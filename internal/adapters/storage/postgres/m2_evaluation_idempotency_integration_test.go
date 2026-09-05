//go:build integration

package postgres_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/google/uuid"
)

func TestM2EvaluationRequestIdentityReservesBudgetOnce(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	requestedAt := uniqueM2MeasurementTime()
	first := newM2EvaluationRun(fixture, requestedAt, evaluationapp.PurposeAuthenticity, 101)
	first.RequestKey = "m2-evaluation-request-001"
	first.RequestHash = strings.Repeat("a", 64)

	created, err := store.CreateEvaluationRun(ctx, first, 100)
	if err != nil {
		t.Fatal(err)
	}
	retry := first
	retry.ID = uuid.New()
	replayed, err := store.CreateEvaluationRun(ctx, retry, 100)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("retry created another run: first=%s retry=%s", created.ID, replayed.ID)
	}

	changed := retry
	changed.RequestHash = strings.Repeat("b", 64)
	if _, err := store.CreateEvaluationRun(ctx, changed, 100); !errors.Is(err, evaluationapp.ErrRequestKeyReused) {
		t.Fatalf("changed request reused the key with error %v", err)
	}

	var reserved, runs int
	if err := store.Pool().QueryRow(ctx, `
		SELECT reserved_samples FROM evaluation_daily_budgets
		WHERE supplier_id=$1 AND model=$2 AND bucket_date=$3::date
	`, fixture.supplierID, fixture.model, requestedAt.Format("2006-01-02")).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM evaluation_runs WHERE request_key=$1", first.RequestKey).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if reserved != 96 || runs != 1 {
		t.Fatalf("request replay changed durable cost: reserved=%d runs=%d", reserved, runs)
	}
}

func TestM2TrustedReferenceRequestIdentityCreatesOneRevision(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	now := uniqueM2MeasurementTime()
	run := newM2EvaluationRun(fixture, now.Add(-time.Hour), evaluationapp.PurposeAuthenticity, 102)
	run, err := store.CreateEvaluationRun(ctx, run, 200)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, "UPDATE evaluation_runs SET status='succeeded', completed_at=$2 WHERE id=$1", run.ID, now); err != nil {
		t.Fatal(err)
	}
	fingerprint := domainevaluation.Fingerprint{
		RunID: run.ID.String(), ProtocolVersion: domainevaluation.ProtocolSingleTokenJSDV1,
		CollectedAt: now.Add(-time.Minute), Cells: map[domainevaluation.CellID]domainevaluation.Distribution{},
		Stability: domainevaluation.Stability{Measured: true, Distance: 0.1},
	}
	if err := store.SaveEvaluationFingerprint(ctx, fingerprint); err != nil {
		t.Fatal(err)
	}
	requestKey := "m2-reference-request-001"
	requestHash := strings.Repeat("c", 64)
	first, err := store.CreateTrustedReference(
		ctx, uuid.New(), run, domainevaluation.ReferenceOfficial, "verified", "operator",
		now, now.Add(7*24*time.Hour), requestKey, requestHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateTrustedReference(
		ctx, uuid.New(), run, domainevaluation.ReferenceOfficial, "verified", "operator",
		now, now.Add(7*24*time.Hour), requestKey, requestHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("reference retry created another revision: first=%s retry=%s", first.ID, replayed.ID)
	}
	if _, err := store.CreateTrustedReference(
		ctx, uuid.New(), run, domainevaluation.ReferenceOfficial, "changed", "operator",
		now, now.Add(7*24*time.Hour), requestKey, strings.Repeat("d", 64),
	); !errors.Is(err, evaluationapp.ErrRequestKeyReused) {
		t.Fatalf("changed reference reused the key with error %v", err)
	}
	var references int
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM trusted_model_references WHERE request_key=$1", requestKey).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if references != 1 {
		t.Fatalf("request replay created %d trusted references", references)
	}
}
