package collection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

type collectionStoreFake struct {
	sites        []SiteAccess
	cursors      map[uuid.UUID]CursorState
	bindings     map[uuid.UUID][]ChannelBinding
	saveErrors   map[uuid.UUID]error
	saved        map[uuid.UUID]measurement.Batch
	sourceLatest map[uuid.UUID]time.Time
	dataGaps     map[uuid.UUID]bool
	failures     map[uuid.UUID]string
	saveCalls    int
	failSaveCall int
	savedBatches []measurement.Batch
}

func (store *collectionStoreFake) ListCollectionSites(context.Context) ([]SiteAccess, error) {
	return append([]SiteAccess(nil), store.sites...), nil
}

func (store *collectionStoreFake) GetCollectionSite(_ context.Context, siteID uuid.UUID) (SiteAccess, error) {
	for _, site := range store.sites {
		if site.ID == siteID {
			return site, nil
		}
	}
	return SiteAccess{}, errors.New("site not found")
}

func (store *collectionStoreFake) GetCollectionCursor(_ context.Context, siteID uuid.UUID) (CursorState, error) {
	return store.cursors[siteID], nil
}

func (store *collectionStoreFake) ListChannelBindings(_ context.Context, siteID uuid.UUID, _, _ time.Time) ([]ChannelBinding, error) {
	return append([]ChannelBinding(nil), store.bindings[siteID]...), nil
}

func (store *collectionStoreFake) SaveMeasurementBatch(
	_ context.Context,
	batch measurement.Batch,
	sourceLatest time.Time,
	dataGap bool,
	scanFrom time.Time,
	scanThrough time.Time,
	_ time.Time,
) (int, int, error) {
	store.saveCalls++
	if store.failSaveCall > 0 && store.saveCalls == store.failSaveCall {
		return 0, 0, errors.New("cursor commit failed")
	}
	if err := store.saveErrors[batch.SiteID]; err != nil {
		return 0, 0, err
	}
	if store.saved == nil {
		store.saved = make(map[uuid.UUID]measurement.Batch)
	}
	if store.sourceLatest == nil {
		store.sourceLatest = make(map[uuid.UUID]time.Time)
	}
	if store.dataGaps == nil {
		store.dataGaps = make(map[uuid.UUID]bool)
	}
	store.saved[batch.SiteID] = batch
	store.savedBatches = append(store.savedBatches, batch)
	if sourceLatest.After(store.sourceLatest[batch.SiteID]) {
		store.sourceLatest[batch.SiteID] = sourceLatest
	}
	persistedGap := dataGap || len(batch.Quarantines) > 0
	store.dataGaps[batch.SiteID] = persistedGap
	state := store.cursors[batch.SiteID]
	if !state.ScannedThrough.Equal(scanFrom) {
		return 0, 0, errors.New("unexpected scan start")
	}
	state.Cursor = batch.Next
	state.ScannedThrough = scanThrough
	state.SourceLatest = store.sourceLatest[batch.SiteID]
	state.DataGap = persistedGap
	store.cursors[batch.SiteID] = state
	return len(batch.Requests), len(batch.Attempts), nil
}

func (store *collectionStoreFake) MarkCollectionFailure(_ context.Context, siteID uuid.UUID, code, _ string, _ time.Time) error {
	if store.failures == nil {
		store.failures = make(map[uuid.UUID]string)
	}
	store.failures[siteID] = code
	return nil
}

func (*collectionStoreFake) ListCollectionStatus(context.Context, *uuid.UUID) ([]Status, error) {
	return nil, nil
}

type collectionVaultFake struct{}

func (collectionVaultFake) Decrypt(credential.Record) ([]byte, error) {
	return []byte("collection-access-token"), nil
}

type logReaderFunc func(context.Context, int, int64, int64, int, int) (RemotePage, error)

func (reader logReaderFunc) Read(ctx context.Context, logType int, start, end int64, page, size int) (RemotePage, error) {
	return reader(ctx, logType, start, end, page, size)
}

type logReaderFactoryFake struct {
	readers map[string]LogReader
}

func (factory logReaderFactoryFake) NewLogReader(baseURL string, _ []byte, _ int64) (LogReader, error) {
	reader, ok := factory.readers[baseURL]
	if !ok {
		return nil, errors.New("reader not found")
	}
	return reader, nil
}

func TestCollectSiteEmptyWindowKeepsCursorAndRecordsSuccessfulRead(t *testing.T) {
	t.Parallel()
	site := collectionSite("empty")
	previous := measurement.Cursor{OccurredAt: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), SourceID: "previous-source"}
	store := &collectionStoreFake{sites: []SiteAccess{site}, cursors: map[uuid.UUID]CursorState{site.ID: {Cursor: previous}}}
	reads := 0
	reader := logReaderFunc(func(context.Context, int, int64, int64, int, int) (RemotePage, error) {
		reads++
		return RemotePage{}, nil
	})
	service, err := NewService(store, collectionVaultFake{}, logReaderFactoryFake{readers: map[string]LogReader{site.BaseURL: reader}}, func() time.Time {
		return time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CollectSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := store.saved[site.ID]
	if !ok || saved.Next != previous || saved.From != previous {
		t.Fatalf("empty batch cursor changed: %#v", saved)
	}
	if reads != 2 || result.ReadRecords != 0 || result.SavedRequests != 0 || result.SavedAttempts != 0 || result.CursorTime == nil || *result.CursorTime != previous.OccurredAt {
		t.Fatalf("empty collection result = %#v reads=%d", result, reads)
	}
	if !store.sourceLatest[site.ID].IsZero() {
		t.Fatalf("empty source unexpectedly advanced: %s", store.sourceLatest[site.ID])
	}
}

func TestWalkLogWindowsReportsUnpageableSingleSecond(t *testing.T) {
	t.Parallel()
	reader := logReaderFunc(func(_ context.Context, _ int, _, _ int64, _, _ int) (RemotePage, error) {
		return RemotePage{Total: int64(maxLogPages*logPageSize + 1)}, nil
	})
	err := walkLogWindows(context.Background(), reader, 5, 5, 5, 60, func(int64, int64, []RemoteLog) error {
		t.Fatal("unpageable window was visited")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "within one second") {
		t.Fatalf("single-second overflow was accepted: %v", err)
	}
}

func TestCollectAllContinuesAfterOneSiteCommitFails(t *testing.T) {
	t.Parallel()
	failedSite := collectionSite("failed")
	successfulSite := collectionSite("successful")
	store := &collectionStoreFake{
		sites:      []SiteAccess{failedSite, successfulSite},
		cursors:    make(map[uuid.UUID]CursorState),
		saveErrors: map[uuid.UUID]error{failedSite.ID: errors.New("cursor commit failed")},
	}
	readers := map[string]LogReader{
		failedSite.BaseURL:     oneSuccessLog("failed-request", 71),
		successfulSite.BaseURL: oneSuccessLog("successful-request", 72),
	}
	service, err := NewService(store, collectionVaultFake{}, logReaderFactoryFake{readers: readers}, func() time.Time {
		return time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CollectAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cursor commit failed") {
		t.Fatalf("site failure was hidden: %v", err)
	}
	if len(result.Sites) != 2 || result.Sites[0].ErrorCode != "collection_failed" || result.Sites[1].SavedRequests != 1 {
		t.Fatalf("sweep result = %#v", result)
	}
	if _, advanced := store.saved[failedSite.ID]; advanced {
		t.Fatal("failed site cursor was advanced")
	}
	if _, saved := store.saved[successfulSite.ID]; !saved {
		t.Fatal("successful site was blocked by another site")
	}
	if store.failures[failedSite.ID] != "measurement_commit_failed" {
		t.Fatalf("stored failure = %q", store.failures[failedSite.ID])
	}
}

func TestCollectSiteQuarantinesPoisonAndAdvancesThroughFollowingLog(t *testing.T) {
	t.Parallel()
	site := collectionSite("poison")
	started := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	store := &collectionStoreFake{sites: []SiteAccess{site}, cursors: make(map[uuid.UUID]CursorState)}
	reader := logReaderFunc(func(_ context.Context, logType int, _, _ int64, page, _ int) (RemotePage, error) {
		if logType != newAPIConsumeLogType || page != 1 {
			return RemotePage{}, nil
		}
		return RemotePage{Items: []RemoteLog{
			{ID: 81, CreatedAt: started.Add(-2 * time.Second).Unix(), Type: newAPIConsumeLogType, Model: "model", DurationSeconds: -1, ChannelID: 91, RequestID: "poison-request", Other: `{"api_key":"must-not-persist"}`},
			{ID: 82, CreatedAt: started.Add(-time.Second).Unix(), Type: newAPIConsumeLogType, Model: "model", DurationSeconds: 1, ChannelID: 92, RequestID: "valid-request"},
		}, Total: 2}, nil
	})
	service, err := NewService(
		store,
		collectionVaultFake{},
		logReaderFactoryFake{readers: map[string]LogReader{site.BaseURL: reader}},
		func() time.Time { return started },
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CollectSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := store.saved[site.ID]
	if result.ReadRecords != 2 || result.SavedRequests != 1 || result.SavedAttempts != 1 || !result.DataGap || !store.dataGaps[site.ID] {
		t.Fatalf("poison collection result = %#v", result)
	}
	if len(batch.Quarantines) != 1 || batch.Quarantines[0].ReasonCode != "invalid_duration" || len(batch.Requests) != 1 || batch.Requests[0].RequestID != "valid-request" {
		t.Fatalf("poison and valid facts were not committed together: %#v", batch)
	}
	if batch.Next.OccurredAt != started.Add(-time.Second) || result.CursorTime == nil || *result.CursorTime != batch.Next.OccurredAt {
		t.Fatalf("cursor did not advance through the valid record after poison: batch=%#v result=%#v", batch.Next, result)
	}
}

func TestCollectSiteResumesAfterCommittedSliceWhenLaterSliceFails(t *testing.T) {
	t.Parallel()
	site := collectionSite("slice-resume")
	started := time.Date(2026, 9, 5, 18, 0, 8, 0, time.UTC)
	initialScan := started.Add(-8 * time.Second)
	store := &collectionStoreFake{
		sites: []SiteAccess{site},
		cursors: map[uuid.UUID]CursorState{site.ID: {
			ScannedThrough: initialScan,
			Overlap:        2 * time.Second,
		}},
		failSaveCall: 2,
	}
	readStarts := make([]int64, 0)
	reader := logReaderFunc(func(_ context.Context, logType int, start, end int64, page, _ int) (RemotePage, error) {
		if page != 1 {
			t.Fatalf("unexpected page %d", page)
		}
		if logType == newAPIConsumeLogType {
			readStarts = append(readStarts, start)
			if end-start > 3 {
				return RemotePage{Total: int64(maxLogPages*logPageSize + 1)}, nil
			}
		}
		return RemotePage{}, nil
	})
	service, err := NewService(
		store,
		collectionVaultFake{},
		logReaderFactoryFake{readers: map[string]LogReader{site.BaseURL: reader}},
		func() time.Time { return started },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CollectSite(context.Background(), site.ID); err == nil || !strings.Contains(err.Error(), "cursor commit failed") {
		t.Fatalf("later slice failure was hidden: %v", err)
	}
	committed := store.cursors[site.ID].ScannedThrough
	if !committed.After(initialScan) || len(store.savedBatches) != 1 {
		t.Fatalf("first slice was not durably committed: scan=%s batches=%d", committed, len(store.savedBatches))
	}

	store.failSaveCall = 0
	readStarts = readStarts[:0]
	if _, err = service.CollectSite(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	wantEarliest := committed.Add(-time.Second).Unix()
	for _, start := range readStarts {
		if start < wantEarliest {
			t.Fatalf("resume restarted before committed overlap: start=%d want>=%d", start, wantEarliest)
		}
	}
	if got := store.cursors[site.ID].ScannedThrough; !got.Equal(started) {
		t.Fatalf("resume scan = %s, want %s", got, started)
	}
}

func collectionSite(suffix string) SiteAccess {
	return SiteAccess{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(suffix)), Name: suffix, BaseURL: "https://" + suffix + ".example", AdminUserID: 1,
	}
}

func oneSuccessLog(requestID string, channelID int64) LogReader {
	return logReaderFunc(func(_ context.Context, logType int, _, _ int64, page, _ int) (RemotePage, error) {
		if logType != newAPIConsumeLogType || page != 1 {
			return RemotePage{}, nil
		}
		return RemotePage{Items: []RemoteLog{{
			ID: channelID, CreatedAt: time.Date(2026, 9, 5, 14, 59, 0, 0, time.UTC).Unix(), Type: newAPIConsumeLogType,
			Model: "model", ChannelID: channelID, RequestID: requestID,
		}}, Total: 1}, nil
	})
}
