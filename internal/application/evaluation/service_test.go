package evaluation

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/google/uuid"
)

func TestRequestRunEnforcesDailySampleBudget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	supplierID := uuid.MustParse("31000000-0000-0000-0000-000000000001")

	t.Run("blocks over budget", func(t *testing.T) {
		store := newFakeStore()
		store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
		store.dailyCount = 105
		dispatcher := &fakeDispatcher{}
		service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

		_, err := service.RequestRun(context.Background(), RunCommand{
			SupplierID: supplierID, Model: "model-a", Purpose: PurposeAuthenticity,
			TargetKind: TargetSupplierDirect, Reason: "manual audit", Actor: "operator",
		})
		if err == nil {
			t.Fatal("request exceeding the daily budget was accepted")
		}
		if len(store.createdRuns) != 0 || len(dispatcher.runIDs) != 0 {
			t.Fatal("over-budget request created or dispatched a run")
		}
		if len(store.dailyLimits) != 1 || store.dailyLimits[0] != dailySampleBudget {
			t.Fatalf("store received daily limits %#v", store.dailyLimits)
		}
	})

	t.Run("allows exact budget boundary", func(t *testing.T) {
		store := newFakeStore()
		store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
		store.dailyCount = 104
		dispatcher := &fakeDispatcher{}
		service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

		run, err := service.RequestRun(context.Background(), RunCommand{
			SupplierID: supplierID, Model: "model-a", Purpose: PurposeAuthenticity,
			TargetKind: TargetSupplierDirect, Reason: "manual audit", Actor: "operator",
		})
		if err != nil {
			t.Fatal(err)
		}
		if run.PlannedSamples != 96 || len(store.createdRuns) != 1 || len(dispatcher.runIDs) != 1 || store.dailyCount != dailySampleBudget {
			t.Fatalf("unexpected accepted run: %#v", run)
		}
	})
}

func TestDispatchFailureIsRetriedAfterNextRetryWithoutReservingBudgetAgain(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	store := newFakeStore()
	supplierID := uuid.MustParse("31500000-0000-0000-0000-000000000001")
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	dispatchFailure := errors.New("queue unavailable")
	dispatcher := &fakeDispatcher{err: dispatchFailure}
	service := newTestService(store, &fakeProber{}, dispatcher, clock)
	command := RunCommand{
		SupplierID: supplierID, Model: "model-a", Purpose: PurposeHealth,
		TargetKind: TargetSupplierDirect, Reason: "scheduled health", Actor: "system",
	}

	run, err := service.RequestRun(context.Background(), command)
	if !errors.Is(err, dispatchFailure) || run.ID == uuid.Nil {
		t.Fatalf("dispatch failure did not preserve the run: run=%#v error=%v", run, err)
	}
	if run.Status != RunUncertain || run.NextRetryAt == nil || !run.NextRetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("run did not record retry time: %#v", run)
	}
	if len(store.createdRuns) != 1 || store.dailyCount != 1 || len(dispatcher.runIDs) != 1 {
		t.Fatalf("unexpected initial state: created=%d budget=%d dispatched=%d", len(store.createdRuns), store.dailyCount, len(dispatcher.runIDs))
	}

	clock.now = now.Add(59 * time.Second)
	if err := service.schedulePurposeIfDue(context.Background(), store.targets[0], PurposeHealth, healthRefreshInterval, clock.now); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.runIDs) != 1 {
		t.Fatal("run was retried before next_retry_at")
	}

	clock.now = now.Add(time.Minute)
	if err := service.schedulePurposeIfDue(context.Background(), store.targets[0], PurposeHealth, healthRefreshInterval, clock.now); !errors.Is(err, dispatchFailure) {
		t.Fatalf("second dispatch failure was not reported: %v", err)
	}
	retried := store.runs[run.ID]
	if retried.NextRetryAt == nil || !retried.NextRetryAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("second failure did not advance retry time: %#v", retried)
	}
	if len(dispatcher.runIDs) != 2 || dispatcher.runIDs[1] != run.ID {
		t.Fatalf("due run was not redispatched after first retry time: %#v", dispatcher.runIDs)
	}

	dispatcher.err = nil
	clock.now = now.Add(2 * time.Minute)
	if err := service.schedulePurposeIfDue(context.Background(), store.targets[0], PurposeHealth, healthRefreshInterval, clock.now); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.runIDs) != 3 || dispatcher.runIDs[2] != run.ID {
		t.Fatalf("rescheduled run was not redispatched: %#v", dispatcher.runIDs)
	}
	if len(store.createdRuns) != 1 || store.dailyCount != 1 || len(store.dailyLimits) != 1 {
		t.Fatalf("retry created a run or reserved budget again: created=%d budget=%d reservations=%d", len(store.createdRuns), store.dailyCount, len(store.dailyLimits))
	}
}

func TestScheduleDueRedispatchesPendingRunWithoutReservingBudgetAgain(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 30, 0, 0, time.UTC)
	supplierID := uuid.MustParse("31500000-0000-0000-0000-000000000002")
	store := newFakeStore()
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	pending := Run{
		ID: uuid.New(), SupplierID: supplierID, Model: "model-a", TargetKind: TargetSupplierDirect,
		Purpose: PurposeHealth, Status: RunPending, RequestedAt: now.Add(-time.Minute),
	}
	store.runs[pending.ID] = pending
	store.createdRuns = []Run{pending}
	dispatcher := &fakeDispatcher{}
	service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

	if err := service.schedulePurposeIfDue(context.Background(), store.targets[0], PurposeHealth, healthRefreshInterval, now); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.runIDs) != 1 || dispatcher.runIDs[0] != pending.ID {
		t.Fatalf("pending run was not recovered: %#v", dispatcher.runIDs)
	}
	if len(store.createdRuns) != 1 || len(store.dailyLimits) != 0 {
		t.Fatal("pending run recovery created another run or budget reservation")
	}
}

func TestScheduleDueRecoversAStaleRunningEvaluation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 35, 0, 0, time.UTC)
	startedAt := now.Add(-runningRecoveryInterval)
	supplierID := uuid.MustParse("31500000-0000-0000-0000-000000000004")
	store := newFakeStore()
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	running := Run{
		ID: uuid.New(), SupplierID: supplierID, Model: "model-a", TargetKind: TargetSupplierDirect,
		Purpose: PurposeHealth, Status: RunRunning, RequestedAt: now.Add(-4 * time.Hour), StartedAt: &startedAt,
	}
	store.runs[running.ID] = running
	store.createdRuns = []Run{running}
	dispatcher := &fakeDispatcher{}
	service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

	if err := service.schedulePurposeIfDue(context.Background(), store.targets[0], PurposeHealth, healthRefreshInterval, now); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.runIDs) != 1 || dispatcher.runIDs[0] != running.ID {
		t.Fatalf("stale running evaluation was not recovered: %#v", dispatcher.runIDs)
	}
	if len(store.createdRuns) != 1 || len(store.dailyLimits) != 0 {
		t.Fatal("running recovery created another run or budget reservation")
	}
}

func TestRequestRunReusesBusinessRequestIdentity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 40, 0, 0, time.UTC)
	supplierID := uuid.MustParse("31500000-0000-0000-0000-000000000003")
	existing := Run{
		ID: uuid.New(), SupplierID: supplierID, Model: "model-a", TargetKind: TargetSupplierDirect,
		Purpose: PurposeHealth, Status: RunRunning, RequestedAt: now.Add(-time.Minute),
		RequestKey: "request-key-001", RequestHash: strings.Repeat("a", 64),
	}
	existing.StartedAt = &now
	store := newFakeStore()
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	store.runs[existing.ID] = existing
	store.createdRuns = []Run{existing}
	dispatcher := &fakeDispatcher{}
	service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

	replayed, err := service.RequestRun(context.Background(), RunCommand{
		SupplierID: supplierID, Model: "model-a", Purpose: PurposeHealth,
		TargetKind: TargetSupplierDirect, Reason: "retry", Actor: "operator",
		RequestKey: existing.RequestKey, RequestHash: existing.RequestHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != existing.ID || len(store.dailyLimits) != 0 || len(dispatcher.runIDs) != 0 {
		t.Fatalf("request identity did not reuse the running evaluation: %#v", replayed)
	}

	_, err = service.RequestRun(context.Background(), RunCommand{
		SupplierID: supplierID, Model: "model-a", Purpose: PurposeHealth,
		TargetKind: TargetSupplierDirect, Reason: "changed retry", Actor: "operator",
		RequestKey: existing.RequestKey, RequestHash: strings.Repeat("b", 64),
	})
	if !errors.Is(err, ErrRequestKeyReused) {
		t.Fatalf("reused request key with changed input returned %v", err)
	}
}

func TestScheduleDuePlansHealthQualityAndEligibleAuthenticity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	referenceSupplierID := uuid.MustParse("32000000-0000-0000-0000-000000000001")
	testedSupplierID := uuid.MustParse("32000000-0000-0000-0000-000000000002")
	noReferenceSupplierID := uuid.MustParse("32000000-0000-0000-0000-000000000003")
	store := newFakeStore()
	store.targets = []TargetAccess{
		testTarget(referenceSupplierID, "shared-model"),
		testTarget(testedSupplierID, "shared-model"),
		testTarget(noReferenceSupplierID, "unreferenced-model"),
	}
	referenceID := uuid.MustParse("32000000-0000-0000-0000-000000000010")
	store.referencesByModel["shared-model"] = &domainevaluation.ModelReference{
		ID: referenceID.String(), Revision: 1, Trust: domainevaluation.ReferenceOfficial,
		Source:    domainevaluation.ModelSubject{SupplierID: referenceSupplierID, Model: "shared-model"},
		ExpiresAt: now.Add(24 * time.Hour),
	}
	dispatcher := &fakeDispatcher{}
	service := newTestService(store, &fakeProber{}, dispatcher, &fakeClock{now: now})

	if err := service.ScheduleDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.createdRuns) != 7 || len(dispatcher.runIDs) != 7 {
		t.Fatalf("created=%d dispatched=%d, want seven of each", len(store.createdRuns), len(dispatcher.runIDs))
	}
	authenticityRuns := make([]Run, 0, 1)
	healthCount := 0
	qualityCount := 0
	for _, run := range store.createdRuns {
		switch run.Purpose {
		case PurposeAuthenticity:
			authenticityRuns = append(authenticityRuns, run)
		case PurposeHealth:
			healthCount++
		case PurposeQuality:
			qualityCount++
		}
	}
	if healthCount != 3 || qualityCount != 3 || len(authenticityRuns) != 1 || authenticityRuns[0].SupplierID != testedSupplierID {
		t.Fatalf("unexpected schedule: health=%d quality=%d authenticity=%#v", healthCount, qualityCount, authenticityRuns)
	}
}

func TestPromoteReferenceEnforcesOfficialReferenceRules(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	supplierID := uuid.MustParse("33000000-0000-0000-0000-000000000001")
	runID := uuid.MustParse("33000000-0000-0000-0000-000000000002")
	baseRun := testRun(runID, supplierID, PurposeAuthenticity, now.Add(-time.Hour))
	baseRun.Status = RunSucceeded
	fingerprint := domainevaluation.Fingerprint{
		RunID: runID.String(), ProtocolVersion: domainevaluation.ProtocolSingleTokenJSDV1,
		CollectedAt: now.Add(-time.Hour), Stability: domainevaluation.Stability{Measured: true, Distance: 0.1},
	}

	tests := []struct {
		name   string
		mutate func(*Run, *domainevaluation.Fingerprint)
	}{
		{name: "pending run", mutate: func(run *Run, _ *domainevaluation.Fingerprint) { run.Status = RunPending }},
		{name: "quality run", mutate: func(run *Run, _ *domainevaluation.Fingerprint) { run.Purpose = PurposeQuality }},
		{name: "site route", mutate: func(run *Run, _ *domainevaluation.Fingerprint) { run.TargetKind = TargetSiteRoute }},
		{name: "missing stability", mutate: func(_ *Run, fingerprint *domainevaluation.Fingerprint) { fingerprint.Stability.Measured = false }},
		{name: "unstable", mutate: func(_ *Run, fingerprint *domainevaluation.Fingerprint) { fingerprint.Stability.Distance = 0.36 }},
		{name: "not a number", mutate: func(_ *Run, fingerprint *domainevaluation.Fingerprint) { fingerprint.Stability.Distance = math.NaN() }},
		{name: "negative distance", mutate: func(_ *Run, fingerprint *domainevaluation.Fingerprint) { fingerprint.Stability.Distance = -0.01 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore()
			run := baseRun
			candidate := fingerprint
			test.mutate(&run, &candidate)
			store.runs[runID] = run
			store.fingerprints[runID] = candidate
			service := newTestService(store, &fakeProber{}, &fakeDispatcher{}, &fakeClock{now: now})
			_, err := service.PromoteReference(context.Background(), ReferenceCommand{
				RunID: runID, Trust: domainevaluation.ReferenceOfficial,
				Reason: "verified official endpoint", Actor: "operator", ValidDays: 7,
			})
			if err == nil {
				t.Fatal("invalid run was promoted")
			}
			if store.createReferenceCalls != 0 {
				t.Fatal("invalid run reached reference persistence")
			}
		})
	}

	store := newFakeStore()
	store.runs[runID] = baseRun
	store.fingerprints[runID] = fingerprint
	service := newTestService(store, &fakeProber{}, &fakeDispatcher{}, &fakeClock{now: now})
	reference, err := service.PromoteReference(context.Background(), ReferenceCommand{
		RunID: runID, Trust: domainevaluation.ReferenceOfficial,
		Reason: "verified official endpoint", Actor: "operator", ValidDays: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reference.Trust != domainevaluation.ReferenceOfficial || reference.Source.SupplierID != supplierID || store.createReferenceCalls != 1 {
		t.Fatalf("unexpected reference: %#v", reference)
	}
}
