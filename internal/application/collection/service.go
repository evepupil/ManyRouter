package collection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

const (
	defaultInitialLookback = 24 * time.Hour
	defaultOverlap         = 10 * time.Minute
	logPageSize            = 100
	maxLogPages            = 100
)

type Service struct {
	store   Store
	vault   Vault
	sources LogReaderFactory
	now     func() time.Time
}

func NewService(store Store, vault Vault, sources LogReaderFactory, now func() time.Time) (*Service, error) {
	if store == nil || vault == nil || sources == nil || now == nil {
		return nil, errors.New("collection dependencies are required")
	}
	return &Service{store: store, vault: vault, sources: sources, now: now}, nil
}

func (service *Service) CollectAll(ctx context.Context) (SweepResult, error) {
	sites, err := service.store.ListCollectionSites(ctx)
	if err != nil {
		return SweepResult{}, err
	}
	result := SweepResult{Sites: make([]Result, 0, len(sites))}
	failures := make([]error, 0)
	for _, site := range sites {
		collected, collectErr := service.collect(ctx, site)
		if collectErr != nil {
			collected = Result{SiteID: site.ID, SiteName: site.Name, ErrorCode: collectionErrorCode(collectErr)}
			failures = append(failures, fmt.Errorf("collect site %s: %w", site.ID, collectErr))
		}
		result.Sites = append(result.Sites, collected)
	}
	return result, errors.Join(failures...)
}

func (service *Service) CollectSite(ctx context.Context, siteID uuid.UUID) (Result, error) {
	if siteID == uuid.Nil {
		return Result{}, errors.New("collection site ID is required")
	}
	site, err := service.store.GetCollectionSite(ctx, siteID)
	if err != nil {
		return Result{}, err
	}
	return service.collect(ctx, site)
}

func (service *Service) ListStatus(ctx context.Context, siteID *uuid.UUID) ([]Status, error) {
	if siteID != nil && *siteID == uuid.Nil {
		return nil, errors.New("collection site ID is invalid")
	}
	return service.store.ListCollectionStatus(ctx, siteID)
}

func (service *Service) collect(ctx context.Context, site SiteAccess) (Result, error) {
	startedAt := service.now().UTC()
	cursor, err := service.store.GetCollectionCursor(ctx, site.ID)
	if err != nil {
		return Result{}, err
	}
	accessToken, err := service.vault.Decrypt(site.Credential)
	if err != nil {
		return Result{}, service.fail(ctx, site.ID, "credential_unavailable", "站点日志凭证无法解密", startedAt, err)
	}
	defer clear(accessToken)
	reader, err := service.sources.NewLogReader(site.BaseURL, accessToken, site.AdminUserID)
	if err != nil {
		return Result{}, service.fail(ctx, site.ID, "source_unavailable", "站点日志读取器无法建立", startedAt, err)
	}

	overlap := cursor.Overlap
	if overlap <= 0 {
		overlap = defaultOverlap
	}
	lowerBound := startedAt.Add(-defaultInitialLookback).Truncate(time.Second)
	scanStart := lowerBound.Unix() - 1
	if !cursor.ScannedThrough.IsZero() {
		scanStart = cursor.ScannedThrough.Unix()
		lowerBound = cursor.ScannedThrough.Add(-overlap).Truncate(time.Second)
	}
	scanEnd := startedAt.Truncate(time.Second).Unix()
	if scanEnd < scanStart {
		return Result{}, service.fail(ctx, site.ID, "invalid_scan_window", "采集扫描水位晚于当前时间", startedAt, errors.New("collection scan watermark is in the future"))
	}

	eventCursor := cursor.Cursor
	committedScan := cursor.ScannedThrough
	resultGap := cursor.DataGap
	result := Result{
		SiteID: site.ID, SiteName: site.Name, DataGap: resultGap,
		CursorTime: timePointer(eventCursor.OccurredAt), ScannedThrough: timePointer(committedScan),
		SourceLatest: timePointer(cursor.SourceLatest),
	}
	sourceLatest := cursor.SourceLatest
	err = walkLogWindows(ctx, reader, lowerBound.Unix(), scanStart, scanEnd, int64(overlap/time.Second), func(windowStart, windowEnd int64, logs []RemoteLog) error {
		inputs, quarantines, latest, parseErr := newAPIMeasurementInputs(site.ID, logs)
		if parseErr != nil {
			return fmt.Errorf("invalid_source_log: %w", parseErr)
		}
		bindings, bindingErr := service.store.ListChannelBindings(
			ctx, site.ID, time.Unix(windowStart, 0).UTC(), time.Unix(windowEnd+1, 0).UTC(),
		)
		if bindingErr != nil {
			return fmt.Errorf("binding_history_unavailable: %w", bindingErr)
		}
		batch, convertErr := convertByRequest(site.ID, eventCursor, inputs, bindings)
		if convertErr != nil {
			return fmt.Errorf("measurement_conversion_failed: %w", convertErr)
		}
		batch.Quarantines = append(batch.Quarantines, quarantines...)
		for _, quarantine := range quarantines {
			if batch.Next.IsZero() || batch.Next.Compare(quarantine.Cursor) < 0 {
				batch.Next = quarantine.Cursor
			}
		}
		if validateErr := batch.Validate(); validateErr != nil {
			return fmt.Errorf("measurement_conversion_failed: %w", validateErr)
		}
		resultGap = resultGap || len(batch.Quarantines) > 0
		nextScan := time.Unix(windowEnd, 0).UTC()
		savedRequests, savedAttempts, saveErr := service.store.SaveMeasurementBatch(
			ctx, batch, latest, false, committedScan, nextScan, startedAt,
		)
		if saveErr != nil {
			return fmt.Errorf("measurement_commit_failed: %w", saveErr)
		}
		eventCursor = batch.Next
		committedScan = nextScan
		result.ReadRecords += len(logs)
		result.SavedRequests += savedRequests
		result.SavedAttempts += savedAttempts
		result.CursorTime = timePointer(eventCursor.OccurredAt)
		result.ScannedThrough = timePointer(committedScan)
		result.DataGap = resultGap
		if latest.After(sourceLatest) {
			sourceLatest = latest
			result.SourceLatest = timePointer(sourceLatest)
		}
		return nil
	})
	if err != nil {
		code := collectionStageErrorCode(err)
		return Result{}, service.fail(ctx, site.ID, code, "站点日志分片采集失败", startedAt, err)
	}
	return result, nil
}

func walkLogWindows(
	ctx context.Context,
	reader LogReader,
	lowerBound int64,
	scanStart int64,
	scanEnd int64,
	overlapSeconds int64,
	visit func(int64, int64, []RemoteLog) error,
) error {
	var walk func(int64, int64) error
	walk = func(from, through int64) error {
		windowStart := from - overlapSeconds + 1
		if windowStart < lowerBound {
			windowStart = lowerBound
		}
		logs, overflow, err := readBoundedLogWindow(ctx, reader, windowStart, through)
		if err != nil {
			return err
		}
		if !overflow {
			return visit(windowStart, through, logs)
		}
		if through-from <= 1 {
			windowStart = through
			logs, overflow, err = readBoundedLogWindow(ctx, reader, windowStart, through)
			if err != nil {
				return err
			}
			if overflow {
				return errors.New("new API log volume exceeds the bounded page limit within one second")
			}
			return visit(windowStart, through, logs)
		}
		middle := from + (through-from)/2
		if err := walk(from, middle); err != nil {
			return err
		}
		return walk(middle, through)
	}
	return walk(scanStart, scanEnd)
}

func readBoundedLogWindow(ctx context.Context, reader LogReader, start, end int64) ([]RemoteLog, bool, error) {
	logs := make([]RemoteLog, 0)
	for _, logType := range []int{newAPIConsumeLogType, newAPIErrorLogType} {
		first, err := reader.Read(ctx, logType, start, end, 1, logPageSize)
		if err != nil {
			return nil, false, err
		}
		if first.Total < 0 {
			return nil, false, errors.New("new API log total is invalid")
		}
		if first.Total > int64(maxLogPages*logPageSize) {
			return nil, true, nil
		}
		windowLogs := append([]RemoteLog(nil), first.Items...)
		for pageNumber := 2; int64((pageNumber-1)*logPageSize) < first.Total; pageNumber++ {
			page, pageErr := reader.Read(ctx, logType, start, end, pageNumber, logPageSize)
			if pageErr != nil {
				return nil, false, pageErr
			}
			if page.Total > int64(maxLogPages*logPageSize) {
				return nil, true, nil
			}
			windowLogs = append(windowLogs, page.Items...)
			if len(page.Items) == 0 || int64(pageNumber*logPageSize) >= page.Total {
				break
			}
		}
		if len(windowLogs) > maxLogPages*logPageSize {
			return nil, true, nil
		}
		logs = append(logs, windowLogs...)
	}
	sort.SliceStable(logs, func(left, right int) bool {
		if logs[left].CreatedAt != logs[right].CreatedAt {
			return logs[left].CreatedAt < logs[right].CreatedAt
		}
		if newAPILogOrder(logs[left].Type) != newAPILogOrder(logs[right].Type) {
			return newAPILogOrder(logs[left].Type) < newAPILogOrder(logs[right].Type)
		}
		return logs[left].ID < logs[right].ID
	})
	return logs, false, nil
}

func collectionStageErrorCode(err error) string {
	for _, code := range []string{
		"invalid_source_log", "binding_history_unavailable", "measurement_conversion_failed", "measurement_commit_failed",
	} {
		if strings.HasPrefix(err.Error(), code+":") {
			return code
		}
	}
	return collectionErrorCode(err)
}

func (service *Service) fail(ctx context.Context, siteID uuid.UUID, code, message string, at time.Time, cause error) error {
	if markErr := service.store.MarkCollectionFailure(ctx, siteID, code, message, at); markErr != nil {
		return errors.Join(cause, markErr)
	}
	return cause
}

func newAPILogOrder(logType int) int {
	if logType == newAPIErrorLogType {
		return 0
	}
	return 1
}

func convertByRequest(
	siteID uuid.UUID,
	from measurement.Cursor,
	inputs []measurement.NewAPILogInput,
	bindings []ChannelBinding,
) (measurement.Batch, error) {
	result := measurement.Batch{
		Source: measurement.SourceRealTraffic, SiteID: siteID, From: from, Next: from,
		Requests: make([]measurement.RequestFact, 0), Attempts: make([]measurement.AttemptFact, 0),
		Quarantines: make([]measurement.QuarantineFact, 0),
	}
	grouped := make(map[string][]measurement.NewAPILogInput)
	order := make([]string, 0)
	for _, input := range inputs {
		if len(grouped[input.RequestID]) == 0 {
			order = append(order, input.RequestID)
		}
		grouped[input.RequestID] = append(grouped[input.RequestID], input)
		if result.Next.IsZero() || result.Next.Compare(input.Cursor) < 0 {
			result.Next = input.Cursor
		}
	}
	for _, requestID := range order {
		records := grouped[requestID]
		converted, err := measurement.ConvertNewAPILogsWithResolver(
			measurement.SourceRealTraffic, siteID, from, records, channelAttributionResolver(bindings),
		)
		if err != nil {
			return measurement.Batch{}, err
		}
		result.Requests = append(result.Requests, converted.Requests...)
		result.Attempts = append(result.Attempts, converted.Attempts...)
		result.Quarantines = append(result.Quarantines, converted.Quarantines...)
	}
	if err := result.Validate(); err != nil {
		return measurement.Batch{}, err
	}
	return result, nil
}

func channelAttributionResolver(bindings []ChannelBinding) measurement.ChannelAttributionResolver {
	return func(channelID int64, at time.Time) measurement.Attribution {
		for _, binding := range bindings {
			if binding.ChannelID == channelID && binding.ActiveAt(at) {
				return measurement.Attribution{
					Status: measurement.AttributionMapped, RelationID: binding.RelationID, SupplierID: binding.SupplierID,
				}
			}
		}
		return measurement.Attribution{Status: measurement.AttributionPending}
	}
}

func collectionErrorCode(err error) string {
	var failure *reconciliation.Failure
	if errors.As(err, &failure) && failure.Code != "" {
		return failure.Code
	}
	return "collection_failed"
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copyOfValue := value.UTC()
	return &copyOfValue
}
