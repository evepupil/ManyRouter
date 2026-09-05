package postgres

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/collection"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrCollectionCursorConflict = errors.New("collection cursor changed")

func (store *Store) ListCollectionSites(ctx context.Context) ([]collection.SiteAccess, error) {
	rows, err := store.queries.ListCollectionSites(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]collection.SiteAccess, 0, len(rows))
	for _, row := range rows {
		result = append(result, collectionSiteAccess(
			row.SiteID, row.SiteCode, row.SiteName, row.NewApiBaseUrl, row.AdminUserID,
			row.CredentialID, row.CredentialPurpose, row.Ciphertext, row.Nonce, row.KeyVersion,
		))
	}
	return result, nil
}

func (store *Store) GetCollectionSite(ctx context.Context, siteID uuid.UUID) (collection.SiteAccess, error) {
	row, err := store.queries.GetCollectionSite(ctx, siteID)
	if err != nil {
		return collection.SiteAccess{}, err
	}
	return collectionSiteAccess(
		row.SiteID, row.SiteCode, row.SiteName, row.NewApiBaseUrl, row.AdminUserID,
		row.CredentialID, row.CredentialPurpose, row.Ciphertext, row.Nonce, row.KeyVersion,
	), nil
}

func collectionSiteAccess(
	siteID uuid.UUID,
	siteCode string,
	siteName string,
	baseURL string,
	adminUserID int64,
	credentialID uuid.UUID,
	purpose string,
	ciphertext []byte,
	nonce []byte,
	keyVersion int32,
) collection.SiteAccess {
	return collection.SiteAccess{
		ID: siteID, Code: siteCode, Name: siteName, BaseURL: baseURL, AdminUserID: adminUserID,
		Credential: credential.Record{
			ID: credentialID, Purpose: credential.Purpose(purpose),
			Ciphertext: append([]byte(nil), ciphertext...), Nonce: append([]byte(nil), nonce...),
			KeyVersion: keyVersion,
		},
	}
}

func (store *Store) GetCollectionCursor(ctx context.Context, siteID uuid.UUID) (collection.CursorState, error) {
	if err := store.queries.EnsureCollectionCursor(ctx, siteID); err != nil {
		return collection.CursorState{}, err
	}
	row, err := store.queries.GetCollectionCursor(ctx, siteID)
	if err != nil {
		return collection.CursorState{}, err
	}
	return mapCollectionCursor(row), nil
}

func mapCollectionCursor(row sqlc.CollectionCursor) collection.CursorState {
	state := collection.CursorState{
		Overlap:    time.Duration(row.OverlapSeconds) * time.Second,
		LastReadAt: timeValue(row.LastReadAt), LastSuccessAt: timeValue(row.LastSuccessAt),
		LastErrorAt: timeValue(row.LastErrorAt), LastErrorCode: textValue(row.LastErrorCode),
		LastErrorMessage: textValue(row.LastErrorMessage), ConsecutiveFailure: int(row.ConsecutiveFailures),
		DataGap: row.DataGap, SourceLatest: unixTimeValue(row.SourceLatestCreatedAt),
	}
	if row.ScannedThroughAt > 0 {
		state.ScannedThrough = time.Unix(row.ScannedThroughAt, 0).UTC()
	}
	if row.CommittedCreatedAt > 0 && row.CommittedSourceID != "" {
		state.Cursor = measurement.Cursor{
			OccurredAt: time.Unix(row.CommittedCreatedAt, 0).UTC(), SourceID: row.CommittedSourceID,
		}
	}
	return state
}

func (store *Store) ListChannelBindings(ctx context.Context, siteID uuid.UUID, start, end time.Time) ([]collection.ChannelBinding, error) {
	rows, err := store.queries.ListChannelBindingHistory(ctx, sqlc.ListChannelBindingHistoryParams{
		SiteID: siteID, WindowStart: databaseTime(start), WindowEnd: databaseTime(end),
	})
	if err != nil {
		return nil, err
	}
	result := make([]collection.ChannelBinding, 0, len(rows))
	for _, row := range rows {
		if !row.ValidFrom.Valid {
			return nil, errors.New("channel binding history time is invalid")
		}
		result = append(result, collection.ChannelBinding{
			ChannelID: row.ExternalChannelID, RelationID: row.RelationID, SupplierID: row.SupplierID,
			ValidFrom: row.ValidFrom.Time.UTC(), ValidTo: optionalTime(row.ValidTo),
		})
	}
	return result, nil
}

func (store *Store) SaveMeasurementBatch(
	ctx context.Context,
	batch measurement.Batch,
	sourceLatest time.Time,
	dataGap bool,
	scanFrom time.Time,
	scanThrough time.Time,
	now time.Time,
) (savedRequests int, savedAttempts int, err error) {
	if err := batch.Validate(); err != nil {
		return 0, 0, err
	}
	if scanThrough.IsZero() || (!scanFrom.IsZero() && scanThrough.Before(scanFrom)) {
		return 0, 0, errors.New("collection scan window is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	if err := queries.EnsureCollectionCursor(ctx, batch.SiteID); err != nil {
		return 0, 0, err
	}
	locked, err := queries.LockCollectionCursor(ctx, batch.SiteID)
	if err != nil {
		return 0, 0, err
	}
	lockedCursor := mapCollectionCursor(locked)
	if lockedCursor.Cursor.Compare(batch.From) != 0 || !lockedCursor.ScannedThrough.Equal(scanFrom) {
		return 0, 0, ErrCollectionCursorConflict
	}

	type requestRevision struct {
		id      uuid.UUID
		current bool
	}
	requestRevisions := make(map[string]requestRevision, len(batch.Requests))
	for _, fact := range batch.Requests {
		requestID, writeRevision, revisionErr := prepareMeasurementRequestRevision(
			ctx, queries, fact, collection.SourceContractVersion, now,
		)
		if revisionErr != nil {
			return 0, 0, revisionErr
		}
		requestRevisions[fact.RequestHash] = requestRevision{id: requestID, current: writeRevision}
		if writeRevision {
			savedRequests++
		}
	}

	for _, fact := range batch.Attempts {
		revision, ok := requestRevisions[fact.RequestHash]
		if !ok {
			return 0, 0, errors.New("measurement attempt request identity is missing")
		}
		if !revision.current {
			continue
		}
		inserted, insertErr := queries.InsertMeasurementAttempt(ctx, sqlc.InsertMeasurementAttemptParams{
			ID:                   measurementAttemptID(fact.SiteID, revision.id, fact.SourceHash),
			RequestMeasurementID: revision.id, AttemptIndex: int32(fact.Ordinal),
			RelationID: databaseUUID(fact.Attribution.RelationID), SupplierID: databaseUUID(fact.Attribution.SupplierID),
			ExternalChannelID: optionalPositiveInt64(fact.ChannelID), AttributionStatus: string(fact.Attribution.Status),
			Model: fact.Model, Outcome: string(fact.Result), IsFinal: fact.IsFinal,
			IsStream: fact.IsStream, StreamCompleted: streamCompletionValue(fact.IsStream, fact.StreamCompletion),
			ProducedVisibleOutput: pgtype.Bool{Bool: fact.ProducedVisibleOutput, Valid: true},
			TtftMs:                optionalInt64(fact.FirstTokenMillis), DurationMs: optionalInt64(fact.TotalMillis),
			DurationResolutionMs: optionalDurationResolution(fact.TotalMillis, fact.DurationResolutionMillis),
			UpstreamStatusCode:   optionalHTTPStatus(fact.HTTPStatus),
			ErrorCategory:        measurementErrorClass(fact.Error), ErrorResponsibility: measurementErrorResponsibility(fact.Error),
			ErrorCode:             optionalText(fact.Error.StableCode),
			ClassificationVersion: measurement.ErrorClassificationRuleVersion,
			ObservedAt:            databaseTime(fact.OccurredAt), RecordedAt: databaseTime(now),
		})
		if insertErr != nil {
			return 0, 0, insertErr
		}
		savedAttempts += int(inserted)
	}
	for _, fact := range batch.Quarantines {
		if _, insertErr := queries.InsertMeasurementQuarantine(ctx, sqlc.InsertMeasurementQuarantineParams{
			ID:     deterministicMeasurementID("quarantine", fact.SiteID, fact.SourceHash),
			SiteID: fact.SiteID, Source: string(fact.Source), SourceEventKey: fact.SourceHash,
			SourceCreatedAt: fact.Cursor.OccurredAt.Unix(), SourceID: fact.Cursor.SourceID,
			ReasonCode: fact.ReasonCode, RecordedAt: databaseTime(now),
		}); insertErr != nil {
			return 0, 0, insertErr
		}
	}
	next := batch.Next
	if next.IsZero() {
		next = batch.From
	}
	committedAt := int64(0)
	committedSourceID := ""
	if !next.IsZero() {
		committedAt = next.OccurredAt.Unix()
		committedSourceID = next.SourceID
	}
	if err := queries.MarkCollectionSuccess(ctx, sqlc.MarkCollectionSuccessParams{
		SiteID: batch.SiteID, CommittedCreatedAt: committedAt, CommittedSourceID: committedSourceID,
		ScannedThroughAt: scanThrough.Unix(), SourceLatestCreatedAt: optionalUnixTime(sourceLatest),
		LastReadAt: databaseTime(now), DataGap: dataGap,
	}); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return savedRequests, savedAttempts, nil
}

func prepareMeasurementRequestRevision(
	ctx context.Context,
	queries *sqlc.Queries,
	fact measurement.RequestFact,
	contract string,
	recordedAt time.Time,
) (uuid.UUID, bool, error) {
	current, err := queries.GetCurrentMeasurementRequestRevision(ctx, sqlc.GetCurrentMeasurementRequestRevisionParams{
		SiteID: databaseUUID(fact.SiteID), Source: string(fact.Source), RequestHash: fact.RequestHash,
	})
	revision := int32(1)
	if err == nil {
		currentCursor := measurement.Cursor{
			OccurredAt: time.Unix(current.TerminalCreatedAt, 0).UTC(), SourceID: current.TerminalSourceID,
		}
		if fact.SourceHash == current.SourceEventKey || fact.TerminalCursor.Compare(currentCursor) < 0 {
			return current.ID, false, nil
		}
		if current.Revision == math.MaxInt32 {
			return uuid.Nil, false, errors.New("measurement request revision limit reached")
		}
		revision = current.Revision + 1
		updated, supersedeErr := queries.SupersedeMeasurementRequest(ctx, sqlc.SupersedeMeasurementRequestParams{
			ID: current.ID, SupersededAt: databaseTime(recordedAt),
		})
		if supersedeErr != nil {
			return uuid.Nil, false, supersedeErr
		}
		if updated != 1 {
			return uuid.Nil, false, errors.New("current measurement request revision changed")
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	requestID := deterministicMeasurementID("request", fact.SiteID, fact.SourceHash)
	inserted, err := insertMeasurementRequestRevision(ctx, queries, requestID, fact, contract, revision, recordedAt)
	if err != nil {
		return uuid.Nil, false, err
	}
	if inserted != 1 {
		return uuid.Nil, false, errors.New("measurement request revision already exists")
	}
	return requestID, true, nil
}

func insertMeasurementRequestRevision(
	ctx context.Context,
	queries *sqlc.Queries,
	requestID uuid.UUID,
	fact measurement.RequestFact,
	contract string,
	revision int32,
	recordedAt time.Time,
) (int64, error) {
	completeness, missingReason := requestCompleteness(fact)
	return queries.InsertMeasurementRequest(ctx, sqlc.InsertMeasurementRequestParams{
		ID: requestID, SiteID: databaseUUID(fact.SiteID), Source: string(fact.Source),
		RequestHash: fact.RequestHash, Revision: revision, SourceContract: contract, SourceEventKey: fact.SourceHash,
		SourceCreatedAt:   pgtype.Int8{Int64: fact.OccurredAt.Unix(), Valid: true},
		TerminalCreatedAt: fact.TerminalCursor.OccurredAt.Unix(), TerminalSourceID: fact.TerminalCursor.SourceID,
		RequestID: optionalText(fact.RequestID), ObservedAt: databaseTime(fact.OccurredAt),
		Model: fact.Model, RequestGroup: fact.Group, Outcome: string(fact.Result),
		FinalRelationID:        databaseUUID(fact.Attribution.RelationID),
		FinalSupplierID:        databaseUUID(fact.Attribution.SupplierID),
		FinalExternalChannelID: optionalPositiveInt64(fact.FinalChannelID),
		AttributionStatus:      string(fact.Attribution.Status), IsStream: fact.IsStream,
		StreamCompleted: streamCompletionValue(fact.IsStream, fact.StreamCompletion),
		TtftMs:          optionalInt64(fact.FirstTokenMillis), DurationMs: optionalInt64(fact.TotalMillis),
		DurationResolutionMs: optionalDurationResolution(fact.TotalMillis, fact.DurationResolutionMillis),
		InputTokens:          pgtype.Int8{Int64: fact.PromptTokens, Valid: true},
		OutputTokens:         pgtype.Int8{Int64: fact.CompletionTokens, Valid: true},
		UpstreamStatusCode:   optionalHTTPStatus(fact.HTTPStatus),
		ErrorCategory:        measurementErrorClass(fact.Error), ErrorResponsibility: measurementErrorResponsibility(fact.Error),
		ErrorCode:             optionalText(fact.Error.StableCode),
		ClassificationVersion: measurement.ErrorClassificationRuleVersion,
		Completeness:          completeness, MissingReason: optionalText(missingReason), RecordedAt: databaseTime(recordedAt),
	})
}

func measurementAttemptID(siteID, requestRevisionID uuid.UUID, sourceHash string) uuid.UUID {
	return deterministicMeasurementID("attempt", siteID, requestRevisionID.String()+"|"+sourceHash)
}

func (store *Store) MarkCollectionFailure(ctx context.Context, siteID uuid.UUID, code, message string, now time.Time) error {
	if err := store.queries.EnsureCollectionCursor(ctx, siteID); err != nil {
		return err
	}
	return store.queries.MarkCollectionFailure(ctx, sqlc.MarkCollectionFailureParams{
		SiteID: siteID, LastReadAt: databaseTime(now), LastErrorCode: optionalText(code),
		LastErrorMessage: optionalText(message),
	})
}

func (store *Store) ResolveMeasurementQuarantine(ctx context.Context, siteID uuid.UUID, sourceHash string, now time.Time) (bool, error) {
	if siteID == uuid.Nil || len(sourceHash) != 64 || now.IsZero() {
		return false, errors.New("measurement quarantine resolution is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	updated, err := queries.ResolveMeasurementQuarantine(ctx, sqlc.ResolveMeasurementQuarantineParams{
		SiteID: siteID, SourceEventKey: sourceHash, ResolvedAt: databaseTime(now),
	})
	if err != nil {
		return false, err
	}
	if err := queries.RefreshCollectionDataGap(ctx, sqlc.RefreshCollectionDataGapParams{
		SiteID: siteID, UpdatedAt: databaseTime(now),
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return updated == 1, nil
}

func (store *Store) ListCollectionStatus(ctx context.Context, siteID *uuid.UUID) ([]collection.Status, error) {
	rows, err := store.queries.ListCollectionStatus(ctx, databaseOptionalUUID(siteID))
	if err != nil {
		return nil, err
	}
	result := make([]collection.Status, 0, len(rows))
	for _, row := range rows {
		status := collection.Status{
			SiteID: row.SiteID, SiteName: row.SiteName, SourceKind: textValue(row.SourceKind),
			ContractVersion: textValue(row.ContractVersion), LastErrorCode: textValue(row.LastErrorCode),
			LastErrorMessage: textValue(row.LastErrorMessage), ConsecutiveFailure: int(row.ConsecutiveFailures.Int32),
			DataGap:    row.DataGap.Valid && row.DataGap.Bool,
			CursorTime: unixTimePointer(row.CommittedCreatedAt), SourceLatest: unixTimePointer(row.SourceLatestCreatedAt),
			ScannedThrough: unixTimePointer(row.ScannedThroughAt),
			LastReadAt:     optionalTime(row.LastReadAt), LastSuccessAt: optionalTime(row.LastSuccessAt),
			LastErrorAt: optionalTime(row.LastErrorAt), UpdatedAt: optionalTime(row.UpdatedAt),
		}
		result = append(result, status)
	}
	return result, nil
}

func deterministicMeasurementID(kind string, siteID uuid.UUID, sourceHash string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"|"+siteID.String()+"|"+sourceHash))
}

func requestCompleteness(fact measurement.RequestFact) (string, string) {
	missing := make([]string, 0, 5)
	if fact.Source == measurement.SourceRealTraffic && strings.TrimSpace(fact.Group) == "" {
		missing = append(missing, "request_group")
	}
	if fact.Attribution.Status != measurement.AttributionMapped {
		missing = append(missing, "supplier_attribution")
	}
	if fact.TotalMillis == nil {
		missing = append(missing, "duration")
	} else if fact.DurationResolutionMillis > measurement.DurationResolutionMillisecond {
		missing = append(missing, "duration_millisecond_precision")
	}
	if fact.IsStream && fact.StreamCompletion == measurement.StreamUnknown {
		missing = append(missing, "stream_completion")
	}
	if len(missing) == 0 {
		return "complete", ""
	}
	if fact.Attribution.Status != measurement.AttributionMapped {
		return "unusable", strings.Join(missing, ",")
	}
	return "partial", strings.Join(missing, ",")
}

func streamCompletionValue(stream bool, completion measurement.StreamCompletion) pgtype.Bool {
	if !stream || completion == measurement.StreamUnknown {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: completion == measurement.StreamCompleted, Valid: true}
}

func measurementErrorClass(fact measurement.ErrorFact) pgtype.Text {
	if fact.Class == "" || fact.Class == measurement.ErrorNone {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(fact.Class), Valid: true}
}

func measurementErrorResponsibility(fact measurement.ErrorFact) pgtype.Text {
	if fact.Class == "" || fact.Class == measurement.ErrorNone {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(fact.ResolvedResponsibility()), Valid: true}
}

func optionalInt64(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func optionalDurationResolution(total *int64, resolution int64) pgtype.Int4 {
	if total == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(resolution), Valid: true}
}

func optionalPositiveInt64(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value > 0}
}

func optionalHTTPStatus(value int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(value), Valid: value >= 100 && value <= 599}
}

func optionalUnixTime(value time.Time) pgtype.Int8 {
	return pgtype.Int8{Int64: value.Unix(), Valid: !value.IsZero()}
}

func unixTimePointer(value pgtype.Int8) *time.Time {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	at := time.Unix(value.Int64, 0).UTC()
	return &at
}

func databaseOptionalUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return databaseUUID(*value)
}

func timeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func unixTimeValue(value pgtype.Int8) time.Time {
	if !value.Valid || value.Int64 <= 0 {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0).UTC()
}

func textValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

var _ collection.Store = (*Store)(nil)
