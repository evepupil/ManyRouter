//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type m2IntegrationFixture struct {
	siteID           uuid.UUID
	supplierID       uuid.UUID
	relationID       uuid.UUID
	managedChannelID uuid.UUID
	model            string
}

func openM2IntegrationStore(t *testing.T) (context.Context, *postgres.Store) {
	t.Helper()
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := "m2_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		_ = admin.Close(context.Background())
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	if err := postgres.Migrate(ctx, parsed.String()); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return ctx, store
}

func createM2IntegrationFixture(t *testing.T, ctx context.Context, store *postgres.Store) m2IntegrationFixture {
	t.Helper()
	fixture := m2IntegrationFixture{}

	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x52}, platformcrypto.MasterKeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	onboard, err := onboarding.NewService(store, vault, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	fixture.model = "m2-model-" + suffix
	siteData, err := onboard.CreateSite(ctx, onboarding.CreateSiteCommand{
		Code: "m2-site-" + suffix, Name: "M2 Integration Site",
		NewAPIBaseURL: "https://gateway.example", NewAPIAccessToken: "m2-admin-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.siteID = siteData.ID
	supplierData, err := onboard.CreateSupplier(ctx, onboarding.CreateSupplierCommand{
		Code: "m2-supplier-" + suffix, Name: "M2 Integration Supplier",
		UpstreamBaseURL: "https://upstream.example/v1", UpstreamAPIKey: "m2-supplier-secret",
		Models: []supplier.ModelInput{{
			Name: fixture.model, UpstreamName: fixture.model,
			InputPrice: decimal.RequireFromString("0.000001"), OutputPrice: decimal.RequireFromString("0.000002"), Currency: "USD",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.supplierID = supplierData.ID
	relation, plan, err := onboard.CreateRelation(ctx, onboarding.CreateRelationCommand{
		SiteID: siteData.ID, SupplierID: supplierData.ID, GroupDisplayName: "M2 Integration Supplier",
		SaleRatio: decimal.NewFromInt(1), Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.relationID = relation.ID
	fixture.managedChannelID = plan.Snapshot.Channel.ID
	return fixture
}

func TestM2ChannelBindingHistoryDoesNotOverlapAfterRebinding(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	firstAt := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	secondAt := firstAt.Add(time.Second)
	if err := store.BindChannel(ctx, fixture.managedChannelID, 101, firstAt); err != nil {
		t.Fatal(err)
	}
	if err := store.BindChannel(ctx, fixture.managedChannelID, 202, secondAt); err != nil {
		t.Fatal(err)
	}

	bindings, err := store.ListChannelBindings(ctx, fixture.siteID, firstAt.Add(-time.Second), secondAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected two channel binding periods, got %#v", bindings)
	}
	first, second := bindings[0], bindings[1]
	if first.ChannelID != 101 || second.ChannelID != 202 || first.RelationID != fixture.relationID || second.RelationID != fixture.relationID {
		t.Fatalf("unexpected channel binding identities: %#v", bindings)
	}
	if !first.ValidFrom.Equal(firstAt) || first.ValidTo == nil || !first.ValidTo.Equal(secondAt) || !second.ValidFrom.Equal(secondAt) || second.ValidTo != nil {
		t.Fatalf("channel binding periods overlap or contain a gap: %#v", bindings)
	}
	if first.ActiveAt(secondAt) || !second.ActiveAt(secondAt) {
		t.Fatalf("channel ownership was ambiguous at the rebind boundary: %#v", bindings)
	}
}

func TestM2MeasurementBatchIsAtomicIdempotentAndAggregatesDurations(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	observedAt := uniqueM2MeasurementTime()
	if err := store.BindChannel(ctx, fixture.managedChannelID, 303, observedAt.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	badBatch := failedM2Batch(fixture, observedAt.Add(-20*time.Second), uuid.New())
	if _, _, err := store.SaveMeasurementBatch(ctx, badBatch, badBatch.Next.OccurredAt, false, time.Time{}, badBatch.Next.OccurredAt, time.Now().UTC()); err == nil {
		t.Fatal("measurement batch with a missing attempt supplier unexpectedly committed")
	}
	var badRequestCount int
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM measurement_requests WHERE source_event_key=$1", badBatch.Requests[0].SourceHash).Scan(&badRequestCount); err != nil {
		t.Fatal(err)
	}
	cursorAfterFailure, err := store.GetCollectionCursor(ctx, fixture.siteID)
	if err != nil {
		t.Fatal(err)
	}
	if badRequestCount != 0 || !cursorAfterFailure.Cursor.IsZero() {
		t.Fatalf("failed batch partially committed: requests=%d cursor=%#v", badRequestCount, cursorAfterFailure.Cursor)
	}

	batch := successfulM2Batch(fixture, observedAt)
	quarantine, err := measurement.NewQuarantineFact(
		measurement.SourceRealTraffic,
		fixture.siteID,
		measurement.Cursor{OccurredAt: observedAt.Add(-5 * time.Second), SourceID: "m2-poison"},
		"invalid_duration",
	)
	if err != nil {
		t.Fatal(err)
	}
	batch.Quarantines = []measurement.QuarantineFact{quarantine}
	savedRequests, savedAttempts, err := store.SaveMeasurementBatch(ctx, batch, batch.Next.OccurredAt, false, time.Time{}, batch.Next.OccurredAt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests != 2 || savedAttempts != 3 {
		t.Fatalf("unexpected saved measurement counts: requests=%d attempts=%d", savedRequests, savedAttempts)
	}
	replay := batch
	replay.From = batch.Next
	savedRequests, savedAttempts, err = store.SaveMeasurementBatch(ctx, replay, replay.Next.OccurredAt, false, batch.Next.OccurredAt, replay.Next.OccurredAt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests != 0 || savedAttempts != 0 {
		t.Fatalf("replayed measurement facts were duplicated: requests=%d attempts=%d", savedRequests, savedAttempts)
	}
	var requestCount, attemptCount int
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM measurement_requests WHERE site_id=$1", fixture.siteID).Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM measurement_attempts attempt
		JOIN measurement_requests request ON request.id=attempt.request_measurement_id
		WHERE request.site_id=$1
	`, fixture.siteID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	cursor, err := store.GetCollectionCursor(ctx, fixture.siteID)
	if err != nil {
		t.Fatal(err)
	}
	var quarantineReason string
	if err := store.Pool().QueryRow(ctx, "SELECT reason_code FROM measurement_quarantines WHERE site_id=$1 AND source_event_key=$2", fixture.siteID, quarantine.SourceHash).Scan(&quarantineReason); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 || attemptCount != 3 || cursor.Cursor.Compare(batch.Next) != 0 || !cursor.DataGap || quarantineReason != "invalid_duration" {
		t.Fatalf("measurement transaction result is inconsistent: requests=%d attempts=%d cursor=%#v", requestCount, attemptCount, cursor.Cursor)
	}
	emptyBatch := measurement.Batch{
		Source: measurement.SourceRealTraffic, SiteID: fixture.siteID, From: batch.Next, Next: batch.Next,
		Requests: []measurement.RequestFact{}, Attempts: []measurement.AttemptFact{}, Quarantines: []measurement.QuarantineFact{},
	}
	if _, _, err := store.SaveMeasurementBatch(ctx, emptyBatch, time.Time{}, false, replay.Next.OccurredAt, emptyBatch.Next.OccurredAt, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stillGapped, err := store.GetCollectionCursor(ctx, fixture.siteID)
	if err != nil {
		t.Fatal(err)
	}
	if !stillGapped.DataGap || !stillGapped.SourceLatest.Equal(batch.Next.OccurredAt) {
		t.Fatalf("clean empty batch cleared quarantine gap or source freshness: %#v", stillGapped)
	}
	resolved, err := store.ResolveMeasurementQuarantine(ctx, fixture.siteID, quarantine.SourceHash, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	resolvedCursor, err := store.GetCollectionCursor(ctx, fixture.siteID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || resolvedCursor.DataGap || !resolvedCursor.SourceLatest.Equal(batch.Next.OccurredAt) {
		t.Fatalf("explicit quarantine resolution did not clear only the gap: resolved=%t cursor=%#v", resolved, resolvedCursor)
	}

	windowStart := observedAt.Truncate(time.Minute)
	windowEnd := windowStart.Add(time.Minute)
	if err := store.RefreshMinuteMetrics(ctx, windowStart, windowStart, windowEnd, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	target := scoringapp.Target{SiteID: fixture.siteID, RelationID: fixture.relationID, SupplierID: fixture.supplierID, Model: fixture.model}
	metrics, err := store.GetWindowMetrics(ctx, target, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.AttemptCount != 3 || metrics.SuccessCount != 1 || metrics.FailureCount != 2 || metrics.SuccessDurationCount != 1 || metrics.FailureDurationCount != 2 || metrics.TTFTCount != 1 {
		t.Fatalf("unexpected minute attempt metrics: %#v", metrics)
	}
	if metrics.TTFT.Counts[2] != 1 || metrics.FailureDuration.Counts[4] != 1 || metrics.SuccessDuration.Counts[5] != 1 || metrics.FailureDuration.Counts[8] != 1 {
		t.Fatalf("measurements did not use the standard latency buckets: ttft=%v success=%v failure=%v", metrics.TTFT.Counts, metrics.SuccessDuration.Counts, metrics.FailureDuration.Counts)
	}
	var requestSuccessCount, requestFailureCount, successDurationSum, failureDurationSum int64
	if err := store.Pool().QueryRow(ctx, `
		SELECT success_duration_count, failure_duration_count,
		       success_duration_sum_ms, failure_duration_sum_ms
		FROM request_metrics_1m
		WHERE bucket_start=$1 AND site_id=$2 AND model=$3 AND source='real_traffic'
	`, windowStart, fixture.siteID, fixture.model).Scan(
		&requestSuccessCount, &requestFailureCount, &successDurationSum, &failureDurationSum,
	); err != nil {
		t.Fatal(err)
	}
	if requestSuccessCount != 1 || requestFailureCount != 1 || successDurationSum != 1_800 || failureDurationSum != 5_200 {
		t.Fatalf("request durations were not separated: success=%d/%d failure=%d/%d", requestSuccessCount, successDurationSum, requestFailureCount, failureDurationSum)
	}
}

func TestM2MeasurementTerminalRevisionSupersedesWithoutDoubleCounting(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	observedAt := uniqueM2MeasurementTime()
	failureDuration, successDuration, firstToken := int64(1_000), int64(2_000), int64(180)
	failure := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: observedAt, SourceID: "0:error"},
		RequestID: "m2-revised-" + fixture.siteID.String(), Result: measurement.FinalFailed,
		Model: fixture.model, Group: "m2-group", CurrentChannelID: 401, UseChannelIDs: []int64{999},
		StableErrorCode: "upstream_timeout", HTTPStatus: 504, ErrorText: "supplier timeout", TotalMillis: &failureDuration,
		DurationResolutionMillis: measurement.DurationResolutionSecond,
	}
	success := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: observedAt, SourceID: "1:consume"},
		RequestID: failure.RequestID, Result: measurement.FinalSucceeded,
		Model: fixture.model, Group: "m2-group", CurrentChannelID: 402, UseChannelIDs: []int64{401},
		HTTPStatus: httpStatusOK, FirstTokenMillis: &firstToken, TotalMillis: &successDuration,
		DurationResolutionMillis: measurement.DurationResolutionSecond,
		PromptTokens:             7, CompletionTokens: 3,
	}
	bindings := map[int64]measurement.ChannelAttribution{
		401: {RelationID: fixture.relationID, SupplierID: fixture.supplierID},
		402: {RelationID: fixture.relationID, SupplierID: fixture.supplierID},
	}
	firstBatch, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, measurement.Cursor{}, []measurement.NewAPILogInput{failure}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, err := store.SaveMeasurementBatch(ctx, firstBatch, observedAt, false, time.Time{}, firstBatch.Next.OccurredAt, time.Now().UTC()); err != nil || savedRequests != 1 || savedAttempts != 2 {
		t.Fatalf("save first terminal revision: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, err)
	}
	currentBatch, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, firstBatch.Next, []measurement.NewAPILogInput{failure, success}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, err := store.SaveMeasurementBatch(ctx, currentBatch, observedAt, false, firstBatch.Next.OccurredAt, currentBatch.Next.OccurredAt, time.Now().UTC()); err != nil || savedRequests != 1 || savedAttempts != 2 {
		t.Fatalf("save current terminal revision: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, err)
	}

	type storedRevision struct {
		id           uuid.UUID
		revision     int
		current      bool
		superseded   bool
		outcome      string
		attemptCount int
	}
	rows, err := store.Pool().Query(ctx, `
		SELECT request.id, request.revision, request.is_current,
		       request.superseded_at IS NOT NULL, request.outcome, COUNT(attempt.id)
		FROM measurement_requests request
		LEFT JOIN measurement_attempts attempt ON attempt.request_measurement_id=request.id
		WHERE request.site_id=$1 AND request.request_hash=$2
		GROUP BY request.id
		ORDER BY request.revision
	`, fixture.siteID, firstBatch.Requests[0].RequestHash)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	revisions := make([]storedRevision, 0, 2)
	for rows.Next() {
		var revision storedRevision
		if err := rows.Scan(&revision.id, &revision.revision, &revision.current, &revision.superseded, &revision.outcome, &revision.attemptCount); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].revision != 1 || revisions[0].current || !revisions[0].superseded || revisions[0].outcome != "failed" || revisions[0].attemptCount != 2 || revisions[1].revision != 2 || !revisions[1].current || revisions[1].superseded || revisions[1].outcome != "succeeded" || revisions[1].attemptCount != 2 {
		t.Fatalf("unexpected stored request revisions: %#v", revisions)
	}
	var requestGroup, completeness, missingReason string
	var durationResolution, coarseAttemptCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT request_group, duration_resolution_ms, completeness, missing_reason
		FROM measurement_requests
		WHERE site_id=$1 AND request_hash=$2 AND is_current
	`, fixture.siteID, firstBatch.Requests[0].RequestHash).Scan(&requestGroup, &durationResolution, &completeness, &missingReason); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM measurement_attempts attempt
		JOIN measurement_requests request ON request.id=attempt.request_measurement_id
		WHERE request.site_id=$1 AND request.request_hash=$2 AND request.is_current
		  AND attempt.duration_resolution_ms=1000
	`, fixture.siteID, firstBatch.Requests[0].RequestHash).Scan(&coarseAttemptCount); err != nil {
		t.Fatal(err)
	}
	if requestGroup != "m2-group" || durationResolution != 1000 || completeness != "partial" || missingReason != "duration_millisecond_precision" || coarseAttemptCount != 2 {
		t.Fatalf("request group or source duration precision was lost: group=%q resolution=%d completeness=%q reason=%q attempts=%d", requestGroup, durationResolution, completeness, missingReason, coarseAttemptCount)
	}

	olderBatch, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, currentBatch.Next, []measurement.NewAPILogInput{failure}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, err := store.SaveMeasurementBatch(ctx, olderBatch, observedAt, false, currentBatch.Next.OccurredAt, olderBatch.Next.OccurredAt, time.Now().UTC()); err != nil || savedRequests != 0 || savedAttempts != 0 {
		t.Fatalf("older terminal replaced the current revision: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, err)
	}
	equalBatch, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, currentBatch.Next, []measurement.NewAPILogInput{failure, success}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, err := store.SaveMeasurementBatch(ctx, equalBatch, observedAt, false, olderBatch.Next.OccurredAt, equalBatch.Next.OccurredAt, time.Now().UTC()); err != nil || savedRequests != 0 || savedAttempts != 0 {
		t.Fatalf("equal terminal was not idempotent: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, err)
	}

	windowStart, windowEnd := observedAt.Truncate(time.Minute), observedAt.Truncate(time.Minute).Add(time.Minute)
	if err := store.RefreshMinuteMetrics(ctx, windowStart, windowStart, windowEnd, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	target := scoringapp.Target{SiteID: fixture.siteID, RelationID: fixture.relationID, SupplierID: fixture.supplierID, Model: fixture.model}
	metrics, err := store.GetWindowMetrics(ctx, target, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.AttemptCount != 2 || metrics.SuccessCount != 1 || metrics.FailureCount != 1 || metrics.PendingAttribution {
		t.Fatalf("historical revision polluted current attempt metrics: %#v", metrics)
	}
	var requestCount, successCount, failureCount int64
	if err := store.Pool().QueryRow(ctx, `
		SELECT request_count, success_count, failure_count
		FROM request_metrics_1m
		WHERE bucket_start=$1 AND site_id=$2 AND model=$3 AND source='real_traffic'
	`, windowStart, fixture.siteID, fixture.model).Scan(&requestCount, &successCount, &failureCount); err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || successCount != 1 || failureCount != 0 {
		t.Fatalf("historical request revision was counted: requests=%d success=%d failure=%d", requestCount, successCount, failureCount)
	}
}

func TestM2MeasurementSameTerminalAddsRevisionWhenEarlierAttemptArrives(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	observedAt := uniqueM2MeasurementTime()
	if err := store.BindChannel(ctx, fixture.managedChannelID, 511, observedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	failure := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: observedAt, SourceID: "0:late-failure"},
		RequestID: "m2-late-attempt-" + fixture.siteID.String(), Result: measurement.FinalFailed,
		Model: fixture.model, CurrentChannelID: 510, HTTPStatus: 504, ErrorText: "upstream timeout",
	}
	terminal := measurement.NewAPILogInput{
		Cursor:    measurement.Cursor{OccurredAt: observedAt.Add(time.Second), SourceID: "1:stable-terminal"},
		RequestID: failure.RequestID, Result: measurement.FinalSucceeded,
		Model: fixture.model, CurrentChannelID: 511, UseChannelIDs: []int64{510}, HTTPStatus: 200,
	}
	bindings := map[int64]measurement.ChannelAttribution{
		510: {RelationID: fixture.relationID, SupplierID: fixture.supplierID},
		511: {RelationID: fixture.relationID, SupplierID: fixture.supplierID},
	}
	partial, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, measurement.Cursor{}, []measurement.NewAPILogInput{terminal}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, saveErr := store.SaveMeasurementBatch(
		ctx, partial, terminal.Cursor.OccurredAt, false, time.Time{}, partial.Next.OccurredAt, time.Now().UTC(),
	); saveErr != nil || savedRequests != 1 || savedAttempts != 2 {
		t.Fatalf("save partial attempt chain: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, saveErr)
	}
	complete, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, partial.Next, []measurement.NewAPILogInput{failure, terminal}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if complete.Requests[0].TerminalCursor.Compare(partial.Requests[0].TerminalCursor) != 0 {
		t.Fatal("terminal cursor changed while adding earlier attempt evidence")
	}
	if savedRequests, savedAttempts, saveErr := store.SaveMeasurementBatch(
		ctx, complete, terminal.Cursor.OccurredAt, false, partial.Next.OccurredAt, complete.Next.OccurredAt, time.Now().UTC(),
	); saveErr != nil || savedRequests != 1 || savedAttempts != 2 {
		t.Fatalf("save completed attempt chain: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, saveErr)
	}
	replay, err := measurement.ConvertNewAPILogs(
		measurement.SourceRealTraffic, fixture.siteID, complete.Next, []measurement.NewAPILogInput{failure, terminal}, bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if savedRequests, savedAttempts, saveErr := store.SaveMeasurementBatch(
		ctx, replay, terminal.Cursor.OccurredAt, false, complete.Next.OccurredAt, replay.Next.OccurredAt, time.Now().UTC(),
	); saveErr != nil || savedRequests != 0 || savedAttempts != 0 {
		t.Fatalf("replay duplicated completed attempt chain: requests=%d attempts=%d error=%v", savedRequests, savedAttempts, saveErr)
	}
	var revisions, currentRevisions, currentAttempts, timeoutAttempts int
	if err := store.Pool().QueryRow(ctx, `
		SELECT COUNT(DISTINCT request.id), COUNT(DISTINCT request.id) FILTER (WHERE request.is_current),
		       COUNT(attempt.id) FILTER (WHERE request.is_current),
		       COUNT(attempt.id) FILTER (WHERE request.is_current AND attempt.error_category='timeout')
		FROM measurement_requests request
		LEFT JOIN measurement_attempts attempt ON attempt.request_measurement_id=request.id
		WHERE request.site_id=$1 AND request.request_hash=$2
	`, fixture.siteID, partial.Requests[0].RequestHash).Scan(&revisions, &currentRevisions, &currentAttempts, &timeoutAttempts); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || currentRevisions != 1 || currentAttempts != 2 || timeoutAttempts != 1 {
		t.Fatalf("late attempt revision state: rows=%d current_rows=%d current_attempts=%d timeouts=%d", revisions, currentRevisions, currentAttempts, timeoutAttempts)
	}
	var completeness, missingReason string
	if err := store.Pool().QueryRow(ctx, `
		SELECT completeness, missing_reason
		FROM measurement_requests
		WHERE site_id=$1 AND request_hash=$2 AND is_current
	`, fixture.siteID, partial.Requests[0].RequestHash).Scan(&completeness, &missingReason); err != nil {
		t.Fatal(err)
	}
	if completeness != "partial" || !strings.Contains(missingReason, "request_group") {
		t.Fatalf("empty request group was treated as complete: completeness=%q missing=%q", completeness, missingReason)
	}
}

func TestM2ConcurrentEvaluationRunsShareDailyBudget(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	requestedAt := uniqueM2MeasurementTime()
	runs := []evaluationapp.Run{
		newM2EvaluationRun(fixture, requestedAt, evaluationapp.PurposeAuthenticity, 1),
		newM2EvaluationRun(fixture, requestedAt, evaluationapp.PurposeQuality, 2),
	}
	start := make(chan struct{})
	results := make(chan error, len(runs))
	for _, run := range runs {
		run := run
		go func() {
			<-start
			_, err := store.CreateEvaluationRun(ctx, run, 100)
			results <- err
		}()
	}
	close(start)

	succeeded, exhausted := 0, 0
	for range runs {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, evaluationapp.ErrDailyBudgetExceeded):
			exhausted++
		default:
			t.Fatalf("unexpected concurrent budget result: %v", err)
		}
	}
	if succeeded != 1 || exhausted != 1 {
		t.Fatalf("daily budget admitted the wrong number of runs: succeeded=%d exhausted=%d", succeeded, exhausted)
	}
	var reserved, runCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT reserved_samples FROM evaluation_daily_budgets
		WHERE supplier_id=$1 AND model=$2 AND bucket_date=$3::date
	`, fixture.supplierID, fixture.model, requestedAt.Format("2006-01-02")).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM evaluation_runs WHERE supplier_id=$1 AND model=$2", fixture.supplierID, fixture.model).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if reserved != 96 || runCount != 1 {
		t.Fatalf("daily budget and run reservations disagree: reserved=%d runs=%d", reserved, runCount)
	}
}

func TestM2ScoreRecommendationsKeepOrderedHistory(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	target := scoringapp.Target{SiteID: fixture.siteID, RelationID: fixture.relationID, SupplierID: fixture.supplierID, Model: fixture.model}
	firstAt := uniqueM2MeasurementTime()
	secondAt := firstAt.Add(time.Hour)
	first := m2ScoreSnapshot(target, firstAt, 72, domainscoring.ConfidenceMedium, domainscoring.AdviceWatch)
	second := m2ScoreSnapshot(target, secondAt, 91, domainscoring.ConfidenceHigh, domainscoring.AdviceJoin)
	if err := store.SaveScoreSnapshot(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScoreSnapshot(ctx, second); err != nil {
		t.Fatal(err)
	}

	previous, err := store.FindPreviousRecommendation(ctx, target, domainscoring.AutoBalanced, secondAt)
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.Score == nil || previous.Score.Float64() != 72 || previous.Confidence != domainscoring.ConfidenceMedium || !previous.CreatedAt.Equal(firstAt) {
		t.Fatalf("first recommendation was not preserved as history: %#v", previous)
	}
	latest, err := store.FindPreviousRecommendation(ctx, target, domainscoring.AutoBalanced, secondAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Score == nil || latest.Score.Float64() != 91 || latest.Confidence != domainscoring.ConfidenceHigh || !latest.CreatedAt.Equal(secondAt) {
		t.Fatalf("latest recommendation was not selected: %#v", latest)
	}
	insights, err := store.ListInsights(ctx, scoringapp.InsightFilter{SiteID: &fixture.siteID, SupplierID: &fixture.supplierID, Model: fixture.model, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if insights.Total != 1 || len(insights.Items) != 1 || len(insights.Items[0].Recommendations) != 1 {
		t.Fatalf("unexpected latest scoring insight: %#v", insights)
	}
	insight := insights.Items[0]
	if insight.TotalScore == nil || *insight.TotalScore != 91 || insight.Recommendations[0].Action != string(domainscoring.AdviceJoin) ||
		insight.PolicyVersion != domainscoring.PolicyVersionM2ShadowV1 || !insight.WindowStart.Equal(second.WindowStart) ||
		!insight.WindowEnd.Equal(second.WindowEnd) || insight.Explanation["source"] != "m2 integration" {
		t.Fatalf("latest scoring insight did not use the second snapshot: %#v", insight)
	}

	scorelessAt := secondAt.Add(time.Hour)
	scoreless := m2ScoreSnapshot(target, scorelessAt, 50, domainscoring.ConfidenceInsufficient, domainscoring.AdviceWatch)
	scoreless.Scores = nil
	scoreless.BalancedScore = nil
	scoreless.Eligibility = "insufficient"
	scoreless.Recommendations[0].CompositeScore = nil
	if err := store.SaveScoreSnapshot(ctx, scoreless); err != nil {
		t.Fatal(err)
	}
	previous, err = store.FindPreviousRecommendation(ctx, target, domainscoring.AutoBalanced, scorelessAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.Score != nil || !previous.CreatedAt.Equal(scorelessAt) {
		t.Fatalf("scoreless recommendation did not break consecutive history: %#v", previous)
	}
}

func TestM2MinuteMetricRefreshTracksRecentOverlapAndDowntime(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	firstEnd := uniqueM2MeasurementTime().Truncate(time.Minute)
	historyStart := firstEnd.Add(-24 * time.Hour)
	computedAt := firstEnd.Add(time.Second)
	if err := store.RefreshMinuteMetrics(ctx, historyStart, firstEnd.Add(-10*time.Minute), firstEnd, computedAt); err != nil {
		t.Fatal(err)
	}
	var initializedAt, factsThrough time.Time
	if err := store.Pool().QueryRow(ctx, `SELECT initialized_at, facts_through FROM scoring_aggregation_state WHERE name='minute_metrics_v1'`).Scan(&initializedAt, &factsThrough); err != nil {
		t.Fatal(err)
	}
	if !initializedAt.Equal(computedAt) || !factsThrough.Equal(firstEnd) {
		t.Fatalf("initial aggregation state = %s / %s", initializedAt, factsThrough)
	}

	sentinelBucket := firstEnd.Add(-30 * time.Minute)
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO request_metrics_1m(bucket_start,site_id,model,source,computed_at)
		VALUES($1,$2,$3,'real_traffic',$4)
	`, sentinelBucket, fixture.siteID, fixture.model, computedAt); err != nil {
		t.Fatal(err)
	}
	secondEnd := firstEnd.Add(5 * time.Minute)
	if err := store.RefreshMinuteMetrics(ctx, secondEnd.Add(-24*time.Hour), secondEnd.Add(-10*time.Minute), secondEnd, secondEnd.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := store.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM request_metrics_1m WHERE bucket_start=$1 AND site_id=$2 AND model=$3)`, sentinelBucket, fixture.siteID, fixture.model).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("normal refresh rebuilt data outside the recent overlap")
	}

	if _, err := store.Pool().Exec(ctx, `UPDATE scoring_aggregation_state SET facts_through=$1 WHERE name='minute_metrics_v1'`, firstEnd.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	thirdEnd := firstEnd.Add(10 * time.Minute)
	if err := store.RefreshMinuteMetrics(ctx, thirdEnd.Add(-24*time.Hour), thirdEnd.Add(-10*time.Minute), thirdEnd, thirdEnd.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM request_metrics_1m WHERE bucket_start=$1 AND site_id=$2 AND model=$3)`, sentinelBucket, fixture.siteID, fixture.model).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("downtime catch-up did not rebuild from the previous overlap")
	}
	if err := store.Pool().QueryRow(ctx, `SELECT initialized_at, facts_through FROM scoring_aggregation_state WHERE name='minute_metrics_v1'`).Scan(&initializedAt, &factsThrough); err != nil {
		t.Fatal(err)
	}
	if !initializedAt.Equal(computedAt) || !factsThrough.Equal(thirdEnd) {
		t.Fatalf("updated aggregation state = %s / %s", initializedAt, factsThrough)
	}
}

func TestM2StoredScoringPolicyMatchesRuntimeVersions(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	var raw []byte
	if err := store.Pool().QueryRow(ctx, "SELECT thresholds FROM scoring_policy_versions WHERE version=$1", domainscoring.PolicyVersionM2ShadowV1).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var thresholds map[string]any
	if err := json.Unmarshal(raw, &thresholds); err != nil {
		t.Fatal(err)
	}
	if thresholds["measurement_rule_version"] != measurement.MeasurementRuleVersion ||
		thresholds["error_classification_version"] != measurement.ErrorClassificationRuleVersion ||
		thresholds["aggregation_version"] != "minute-metrics-v1" ||
		thresholds["collection_stale_seconds"] != float64(3600) ||
		thresholds["recommendation_max_gap_seconds"] != float64(600) {
		t.Fatalf("stored scoring policy drifted from runtime versions: %#v", thresholds)
	}
}

func TestM2ScoringTargetsAndLowestPriceOnlyUseAvailablePlacements(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	now := uniqueM2MeasurementTime()
	if _, err := store.Pool().Exec(ctx, `UPDATE site_suppliers SET desired_status='enabled',sync_status='active',updated_at=$2 WHERE id=$1`, fixture.relationID, now); err != nil {
		t.Fatal(err)
	}
	strategyID := uuid.New()
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO site_strategies(id,site_id,kind,group_key,display_name,enabled,visible,version,created_at,updated_at)
		VALUES($1,$2,'balanced',$3,'Balanced',false,true,1,$4,$4)
	`, strategyID, fixture.siteID, "m2-balanced-"+strategyID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO strategy_members(strategy_id,relation_id) VALUES($1,$2)`, strategyID, fixture.relationID); err != nil {
		t.Fatal(err)
	}
	targets, err := store.ListScoringTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].SyncStatus != "active" || len(targets[0].CurrentStrategies) != 0 {
		t.Fatalf("available scoring targets = %#v", targets)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE site_strategies SET enabled=true,updated_at=$2 WHERE id=$1`, strategyID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	targets, err = store.ListScoringTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || len(targets[0].CurrentStrategies) != 1 || targets[0].CurrentStrategies[0] != domainscoring.AutoBalanced {
		t.Fatalf("enabled strategy membership = %#v", targets)
	}
	lowest, err := store.GetLowestPeerCost(ctx, fixture.siteID, fixture.model, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !lowest.Equal(decimal.RequireFromString("0.0000015")) {
		t.Fatalf("lowest available cost = %s", lowest)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE site_suppliers SET sync_status='failed',updated_at=$2 WHERE id=$1`, fixture.relationID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	targets, err = store.ListScoringTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lowest, err = store.GetLowestPeerCost(ctx, fixture.siteID, fixture.model, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 || !lowest.IsZero() {
		t.Fatalf("unavailable placement leaked into scoring: targets=%#v lowest=%s", targets, lowest)
	}
}

func TestM2PriceEvidenceUsesContinuousVersionedHistory(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	start := uniqueM2MeasurementTime()
	firstChange := start.Add(time.Hour)
	secondChange := start.Add(2 * time.Hour)
	end := start.Add(24 * time.Hour)
	if _, err := store.Pool().Exec(ctx, `UPDATE supplier_models SET input_price='0.000002',output_price='0.000004',updated_at=$3 WHERE supplier_id=$1 AND model=$2`, fixture.supplierID, fixture.model, firstChange); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE supplier_models SET input_price='0.000004',output_price='0.000008',updated_at=$3 WHERE supplier_id=$1 AND model=$2`, fixture.supplierID, fixture.model, secondChange); err != nil {
		t.Fatal(err)
	}
	target := scoringapp.Target{
		SupplierID: fixture.supplierID, Model: fixture.model, Currency: "USD",
		InputPrice: decimal.RequireFromString("0.000004"), OutputPrice: decimal.RequireFromString("0.000008"),
	}
	evidence, err := store.GetPriceEvidence(ctx, target, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.Available || evidence.ChangesPerDay != 2 || math.Abs(evidence.ChangeMagnitudeRatio-0.5) > 1e-9 {
		t.Fatalf("price evidence = %#v", evidence)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE supplier_models SET currency='EUR',updated_at=$3 WHERE supplier_id=$1 AND model=$2`, fixture.supplierID, fixture.model, secondChange.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	target.Currency = "EUR"
	evidence, err = store.GetPriceEvidence(ctx, target, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Available {
		t.Fatalf("currency switch produced usable price evidence: %#v", evidence)
	}
}

func TestM2FailureStreakAndRecoveryUseBusinessEvents(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	start := uniqueM2MeasurementTime()
	completed := true
	siteID := fixture.siteID
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "auth-retried", []m2ScoringAttempt{
		{At: start.Add(time.Second), Outcome: "failed", ErrorCategory: "authentication", Stream: true},
		{At: start.Add(2 * time.Second), Outcome: "failed", ErrorCategory: "authentication", Stream: true},
	})
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "balance", []m2ScoringAttempt{{
		At: start.Add(3 * time.Second), Outcome: "failed", ErrorCategory: "balance_exhausted",
	}})
	target := scoringapp.Target{SiteID: fixture.siteID, SupplierID: fixture.supplierID, Model: fixture.model}
	streak, err := store.GetFailureStreak(ctx, target, start.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if streak.Total != 2 || streak.Authentication != 1 || streak.Balance != 1 {
		t.Fatalf("deduplicated failure streak = %#v", streak)
	}

	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "recovered-request", []m2ScoringAttempt{
		{At: start.Add(5 * time.Second), Outcome: "failed", ErrorCategory: "timeout", Stream: true},
		{At: start.Add(8 * time.Second), Outcome: "succeeded", Stream: true, Produced: true, StreamCompleted: &completed},
	})
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "ongoing-failure", []m2ScoringAttempt{{
		At: start.Add(10 * time.Second), Outcome: "failed", ErrorCategory: "timeout", Stream: true,
	}})
	streak, err = store.GetFailureStreak(ctx, target, start.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if streak.Total != 1 {
		t.Fatalf("site success did not reset failures: %#v", streak)
	}
	insertM2ScoringRequest(t, ctx, store, fixture, nil, measurement.SourceDirectProbe, "direct-recovery", []m2ScoringAttempt{{
		At: start.Add(12 * time.Second), Outcome: "succeeded",
	}})
	streak, err = store.GetFailureStreak(ctx, target, start.Add(13*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if streak != (scoringapp.FailureStreak{}) {
		t.Fatalf("direct probe success did not reset failures: %#v", streak)
	}

	end := start.Add(20 * time.Second)
	if err := store.RefreshMinuteMetrics(ctx, start, start, end, end.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	metrics, err := store.GetWindowMetrics(ctx, target, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RecoveryMillis != 10_000 || metrics.StreamCount != 1 || metrics.StreamCompletedCount != 1 {
		t.Fatalf("recovery or stream metrics = %#v", metrics)
	}
}

func TestM2SupplierSLAUsesOnlyConfirmedSupplierFailures(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	start := uniqueM2MeasurementTime()
	siteID := fixture.siteID
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "user-error", []m2ScoringAttempt{{
		At: start.Add(time.Second), Outcome: "failed", ErrorCategory: "invalid_request", Responsibility: string(measurement.ResponsibilityUser),
	}})
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "unknown-error", []m2ScoringAttempt{{
		At: start.Add(2 * time.Second), Outcome: "failed", ErrorCategory: "unknown", Responsibility: string(measurement.ResponsibilityUnknown),
	}})
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "supplier-error", []m2ScoringAttempt{{
		At: start.Add(3 * time.Second), Outcome: "failed", ErrorCategory: "upstream_unavailable", Responsibility: string(measurement.ResponsibilitySupplier),
	}})
	insertM2ScoringRequest(t, ctx, store, fixture, &siteID, measurement.SourceRealTraffic, "success", []m2ScoringAttempt{{
		At: start.Add(4 * time.Second), Outcome: "succeeded",
	}})
	end := start.Add(time.Minute)
	if err := store.RefreshMinuteMetrics(ctx, start, start, end, end.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	target := scoringapp.Target{SiteID: fixture.siteID, SupplierID: fixture.supplierID, Model: fixture.model}
	metrics, err := store.GetWindowMetrics(ctx, target, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.AttemptCount != 4 || metrics.SLAAttemptCount != 2 || metrics.SLAFailureCount != 1 || !metrics.PendingAttribution {
		t.Fatalf("responsibility-aware SLA metrics = %#v", metrics)
	}
}

type m2ScoringAttempt struct {
	At              time.Time
	Outcome         string
	ErrorCategory   string
	Responsibility  string
	Stream          bool
	Produced        bool
	StreamCompleted *bool
}

func insertM2ScoringRequest(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	fixture m2IntegrationFixture,
	siteID *uuid.UUID,
	source measurement.Source,
	key string,
	attempts []m2ScoringAttempt,
) {
	t.Helper()
	if len(attempts) == 0 {
		t.Fatal("scoring request requires attempts")
	}
	requestID := uuid.New()
	requestHash := m2Hash("request-" + key)
	finalAttempt := attempts[len(attempts)-1]
	var databaseSite, relationID, channelID any
	group := ""
	if siteID != nil {
		databaseSite = *siteID
		relationID = fixture.relationID
		channelID = int64(303)
		group = "m2-group"
	}
	var requestError any
	var requestResponsibility any
	if finalAttempt.Outcome != "succeeded" {
		requestError = finalAttempt.ErrorCategory
		requestResponsibility = finalAttempt.Responsibility
		if requestResponsibility == "" {
			requestResponsibility = string(measurement.ResponsibilitySupplier)
		}
	}
	var requestTTFT any
	if finalAttempt.Produced {
		requestTTFT = int64(10)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO measurement_requests(
			id,site_id,source,request_hash,revision,source_contract,source_event_key,
			terminal_created_at,terminal_source_id,request_id,observed_at,model,request_group,
			outcome,final_relation_id,final_supplier_id,final_external_channel_id,attribution_status,
			is_stream,stream_completed,ttft_ms,duration_ms,duration_resolution_ms,error_category,
			error_responsibility,classification_version,completeness,recorded_at
		) VALUES(
			$1,$2,$3,$4,1,'m2-test',$5,
			$6,$7,$8,$9,$10,$11,
			$12,$13,$14,$15,'mapped',
			$16,$17,$18,100,1,$19,
			$20,$21,'complete',$22
		)
	`, requestID, databaseSite, string(source), requestHash, m2Hash("event-"+key),
		finalAttempt.At.Unix(), "terminal-"+key, "request-"+key, attempts[0].At, fixture.model, group,
		finalAttempt.Outcome, relationID, fixture.supplierID, channelID, finalAttempt.Stream,
		finalAttempt.StreamCompleted, requestTTFT, requestError, requestResponsibility,
		measurement.ErrorClassificationRuleVersion, finalAttempt.At); err != nil {
		t.Fatal(err)
	}
	for index, attempt := range attempts {
		var errorCategory any
		var responsibility any
		if attempt.Outcome != "succeeded" {
			errorCategory = attempt.ErrorCategory
			responsibility = attempt.Responsibility
			if responsibility == "" {
				responsibility = string(measurement.ResponsibilitySupplier)
			}
		}
		var ttft any
		if attempt.Produced {
			ttft = int64(10)
		}
		if _, err := store.Pool().Exec(ctx, `
			INSERT INTO measurement_attempts(
				id,request_measurement_id,attempt_index,relation_id,supplier_id,external_channel_id,
				attribution_status,model,outcome,is_final,is_stream,stream_completed,produced_visible_output,
				ttft_ms,duration_ms,duration_resolution_ms,error_category,error_responsibility,classification_version,observed_at,recorded_at
			) VALUES($1,$2,$3,$4,$5,$6,'mapped',$7,$8,$9,$10,$11,$12,$13,100,1,$14,$15,$16,$17,$17)
		`, uuid.New(), requestID, index+1, relationID, fixture.supplierID, channelID,
			fixture.model, attempt.Outcome, index == len(attempts)-1, attempt.Stream,
			attempt.StreamCompleted, attempt.Produced, ttft, errorCategory, responsibility,
			measurement.ErrorClassificationRuleVersion, attempt.At); err != nil {
			t.Fatal(err)
		}
	}
}

func successfulM2Batch(fixture m2IntegrationFixture, observedAt time.Time) measurement.Batch {
	firstToken, successDuration, timeoutDuration, limitedDuration := int64(150), int64(1_700), int64(750), int64(5_200)
	successRequestHash := m2Hash("success-request-" + fixture.siteID.String())
	failureRequestHash := m2Hash("failure-request-" + fixture.siteID.String())
	attribution := measurement.Attribution{Status: measurement.AttributionMapped, RelationID: fixture.relationID, SupplierID: fixture.supplierID}
	timeoutError := measurement.ErrorFact{Class: measurement.ErrorTimeout, StableCode: "upstream_timeout", Summary: "supplier request timed out", RuleVersion: measurement.ErrorClassificationRuleVersion}
	limitedError := measurement.ErrorFact{Class: measurement.ErrorRateLimited, StableCode: "upstream_rate_limited", Summary: "supplier rate limited the request", RuleVersion: measurement.ErrorClassificationRuleVersion}
	requests := []measurement.RequestFact{
		{
			SourceHash: m2Hash("success-source-" + fixture.siteID.String()), RequestHash: successRequestHash,
			RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
			RequestID:      "m2-success-" + fixture.siteID.String(),
			TerminalCursor: measurement.Cursor{OccurredAt: observedAt.Add(10 * time.Second), SourceID: "m2-success"}, Model: fixture.model, Group: "m2-group",
			Result: measurement.FinalSucceeded, HTTPStatus: httpStatusOK, FinalChannelID: 303, Attribution: attribution,
			StreamCompletion: measurement.StreamUnknown,
			FirstTokenMillis: &firstToken, TotalMillis: m2Int64(1_800), DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			PromptTokens: 8, CompletionTokens: 5,
			TotalTokens: 13, AttemptCount: 2, OccurredAt: observedAt.Add(10 * time.Second),
		},
		{
			SourceHash: m2Hash("failure-source-" + fixture.siteID.String()), RequestHash: failureRequestHash,
			RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
			RequestID:      "m2-failure-" + fixture.siteID.String(),
			TerminalCursor: measurement.Cursor{OccurredAt: observedAt.Add(20 * time.Second), SourceID: "m2-failure"}, Model: fixture.model, Group: "m2-group",
			Result: measurement.FinalFailed, HTTPStatus: 429, FinalChannelID: 303, Attribution: attribution,
			StreamCompletion: measurement.StreamUnknown,
			TotalMillis:      &limitedDuration, DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			AttemptCount: 1, OccurredAt: observedAt.Add(20 * time.Second), Error: limitedError,
		},
	}
	attempts := []measurement.AttemptFact{
		{
			SourceHash: m2Hash("timeout-attempt-" + fixture.siteID.String()), RequestHash: successRequestHash,
			RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
			RequestID: requests[0].RequestID, Ordinal: 1, ChannelID: 303, Model: fixture.model, Attribution: attribution,
			Result: measurement.AttemptFailed, HTTPStatus: 504, TotalMillis: &timeoutDuration,
			StreamCompletion:         measurement.StreamUnknown,
			DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			OccurredAt:               observedAt.Add(5 * time.Second), Error: timeoutError,
		},
		{
			SourceHash: m2Hash("success-attempt-" + fixture.siteID.String()), RequestHash: successRequestHash,
			RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
			RequestID: requests[0].RequestID, Ordinal: 2, ChannelID: 303, Model: fixture.model, Attribution: attribution,
			Result: measurement.AttemptSucceeded, HTTPStatus: httpStatusOK, IsFinal: true, ProducedVisibleOutput: true,
			StreamCompletion: measurement.StreamUnknown,
			FirstTokenMillis: &firstToken, TotalMillis: &successDuration, DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			OccurredAt: observedAt.Add(10 * time.Second),
		},
		{
			SourceHash: m2Hash("limited-attempt-" + fixture.siteID.String()), RequestHash: failureRequestHash,
			RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
			RequestID: requests[1].RequestID, Ordinal: 1, ChannelID: 303, Model: fixture.model, Attribution: attribution,
			Result: measurement.AttemptFailed, HTTPStatus: 429, IsFinal: true, TotalMillis: &limitedDuration,
			StreamCompletion:         measurement.StreamUnknown,
			DurationResolutionMillis: measurement.DurationResolutionMillisecond,
			OccurredAt:               observedAt.Add(20 * time.Second), Error: limitedError,
		},
	}
	return measurement.Batch{
		Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
		Next:     measurement.Cursor{OccurredAt: requests[1].OccurredAt, SourceID: "m2-final"},
		Requests: requests, Attempts: attempts,
	}
}

func failedM2Batch(fixture m2IntegrationFixture, observedAt time.Time, missingSupplierID uuid.UUID) measurement.Batch {
	duration := int64(900)
	requestHash := m2Hash("rollback-request-" + fixture.siteID.String())
	errorFact := measurement.ErrorFact{Class: measurement.ErrorTimeout, StableCode: "upstream_timeout", Summary: "supplier request timed out", RuleVersion: measurement.ErrorClassificationRuleVersion}
	request := measurement.RequestFact{
		SourceHash: m2Hash("rollback-source-" + fixture.siteID.String()), RequestHash: requestHash,
		RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
		RequestID:      "m2-rollback-" + fixture.siteID.String(),
		TerminalCursor: measurement.Cursor{OccurredAt: observedAt, SourceID: "m2-rollback"}, Model: fixture.model, Group: "m2-group",
		Result: measurement.FinalFailed, HTTPStatus: 504, FinalChannelID: 303,
		StreamCompletion: measurement.StreamUnknown,
		Attribution:      measurement.Attribution{Status: measurement.AttributionMapped, RelationID: fixture.relationID, SupplierID: fixture.supplierID},
		TotalMillis:      &duration, DurationResolutionMillis: measurement.DurationResolutionMillisecond,
		AttemptCount: 1, OccurredAt: observedAt, Error: errorFact,
	}
	attempt := measurement.AttemptFact{
		SourceHash: m2Hash("rollback-attempt-" + fixture.siteID.String()), RequestHash: requestHash,
		RuleVersion: measurement.MeasurementRuleVersion, Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
		RequestID: request.RequestID, Ordinal: 1, ChannelID: 303, Model: fixture.model,
		Attribution: measurement.Attribution{Status: measurement.AttributionMapped, RelationID: fixture.relationID, SupplierID: missingSupplierID},
		Result:      measurement.AttemptFailed, HTTPStatus: 504, IsFinal: true, TotalMillis: &duration,
		StreamCompletion:         measurement.StreamUnknown,
		DurationResolutionMillis: measurement.DurationResolutionMillisecond,
		OccurredAt:               observedAt, Error: errorFact,
	}
	return measurement.Batch{
		Source: measurement.SourceRealTraffic, SiteID: fixture.siteID,
		Next:     measurement.Cursor{OccurredAt: observedAt, SourceID: "m2-rollback"},
		Requests: []measurement.RequestFact{request}, Attempts: []measurement.AttemptFact{attempt},
	}
}

func newM2EvaluationRun(fixture m2IntegrationFixture, requestedAt time.Time, purpose evaluationapp.Purpose, seed uint64) evaluationapp.Run {
	return evaluationapp.Run{
		ID: uuid.New(), SupplierID: fixture.supplierID, Model: fixture.model, UpstreamModel: fixture.model,
		TargetKind: evaluationapp.TargetSupplierDirect, Purpose: purpose,
		SuiteVersion: evaluationapp.FingerprintSuiteVersion, AlgorithmVersion: evaluationapp.AlgorithmVersion,
		Seed: seed, PlannedSamples: 96, RequestedBy: "m2-integration", RequestReason: "verify daily budget",
		RequestedAt: requestedAt,
	}
}

func m2ScoreSnapshot(
	target scoringapp.Target,
	createdAt time.Time,
	score domainscoring.Score,
	confidence domainscoring.Confidence,
	action domainscoring.AdviceAction,
) scoringapp.Snapshot {
	dimensions := &domainscoring.DimensionScores{Price: score, Latency: score, SLA: score, Quality: score}
	factsThrough := createdAt.Add(-time.Minute)
	reason := domainscoring.AdviceJoinThresholdMet
	if action == domainscoring.AdviceWatch {
		reason = domainscoring.AdviceJoinConfirmationPending
	}
	return scoringapp.Snapshot{
		ID: uuid.New(), Target: target,
		WindowStart: createdAt.Add(-24 * time.Hour), WindowEnd: createdAt,
		FactsThrough: &factsThrough, PassiveSamples: 60, ActiveSamples: 96,
		Scores: dimensions, BalancedScore: &score, Confidence: confidence, Eligibility: "eligible",
		HardReasons: []domainscoring.GateReason{}, Explanation: map[string]any{"source": "m2 integration"},
		CreatedAt: createdAt,
		Recommendations: []domainscoring.ShadowAdvice{{
			Action: action, PolicyVersion: domainscoring.PolicyVersionM2ShadowV1,
			AutoKind: domainscoring.AutoBalanced, CompositeScore: &score, Confidence: confidence,
			Reasons: []domainscoring.AdviceReason{reason},
		}},
	}
}

func uniqueM2MeasurementTime() time.Time {
	id := uuid.New()
	offset := int64(id[0])<<24 | int64(id[1])<<16 | int64(id[2])<<8 | int64(id[3])
	return time.Date(2080, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(offset%500_000) * time.Minute)
}

func m2Hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func m2Int64(value int64) *int64 {
	return &value
}

const httpStatusOK = 200
