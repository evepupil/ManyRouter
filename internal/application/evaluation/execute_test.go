package evaluation

import (
	"context"
	"testing"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

func TestAuthenticityPlanContainsTwelveSamplesForAllEightCells(t *testing.T) {
	t.Parallel()
	run := Run{Purpose: PurposeAuthenticity, PlannedSamples: 96, Seed: 42}
	plan, err := evaluationPlan(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 96 || plannedSamples(PurposeAuthenticity) != 96 {
		t.Fatalf("got plan=%d planned=%d, want 96", len(plan), plannedSamples(PurposeAuthenticity))
	}

	counts := make(map[domainevaluation.CellID]int)
	identities := make(map[string]struct{}, len(plan))
	for _, probe := range plan {
		counts[probe.cell]++
		identity := sampleIdentity(probe.key, probe.index)
		if _, duplicate := identities[identity]; duplicate {
			t.Fatalf("duplicate probe identity %q", identity)
		}
		identities[identity] = struct{}{}
		if probe.prompt == "" || probe.temperature != 1 || probe.topP != 1 || probe.maxTokens != 16 || probe.stream {
			t.Fatalf("unexpected fingerprint probe: %#v", probe)
		}
	}
	for _, cell := range domainevaluation.RequiredCells() {
		if counts[cell] != 12 {
			t.Fatalf("cell %q has %d samples, want 12", cell, counts[cell])
		}
	}
}

func TestFailedProbeDoesNotBecomeAnAuthenticityAnswer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	prober := &fakeProber{probe: func(ProbeRequest) (ProbeResult, error) {
		return ProbeResult{Text: "42", ResponseModel: "model-a", HTTPStatus: 500, TotalMillis: 12}, nil
	}}
	service := newTestService(store, prober, &fakeDispatcher{}, &fakeClock{now: now})
	run := testRun(uuid.MustParse("34000000-0000-0000-0000-000000000001"), uuid.MustParse("34000000-0000-0000-0000-000000000002"), PurposeAuthenticity, now)
	target := testTarget(run.SupplierID, run.Model)

	sample, _, _, err := service.executeProbe(context.Background(), run, target, []byte("key"), plannedProbe{
		key: string(domainevaluation.CellNumber100EN), cell: domainevaluation.CellNumber100EN,
		index: 0, prompt: "probe", temperature: 1, topP: 1, maxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sample.Outcome != "failed" || sample.NormalizedAnswer != "" {
		t.Fatalf("failed probe became an answer: %#v", sample)
	}
}

func TestIncompleteStreamProbeIsSavedAsFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	prober := &fakeProber{probe: func(ProbeRequest) (ProbeResult, error) {
		return ProbeResult{
			Text: "95", ResponseModel: "model-a", HTTPStatus: 200,
			FinishReason: "stop", TotalMillis: 12, StreamCompleted: false,
		}, nil
	}}
	service := newTestService(store, prober, &fakeDispatcher{}, &fakeClock{now: now})
	run := testRun(uuid.MustParse("34500000-0000-0000-0000-000000000001"), uuid.MustParse("34500000-0000-0000-0000-000000000002"), PurposeHealth, now)
	target := testTarget(run.SupplierID, run.Model)

	sample, requestFact, attemptFact, err := service.executeProbe(context.Background(), run, target, []byte("key"), plannedProbe{
		key: "stream_health", index: 0, prompt: "probe", expected: "95",
		stream: true, topP: 1, maxTokens: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sample.Outcome != "failed" || sample.NormalizedAnswer != "" || sample.StreamCompleted == nil || *sample.StreamCompleted {
		t.Fatalf("incomplete stream was accepted: %#v", sample)
	}
	if sample.Error.Class != measurement.ErrorStreamIncomplete || sample.Error.StableCode != "stream_incomplete" {
		t.Fatalf("unexpected incomplete-stream classification: %#v", sample.Error)
	}
	if requestFact.Result != measurement.FinalIncomplete || requestFact.StreamCompletion != measurement.StreamIncomplete || attemptFact.Result != measurement.AttemptIncomplete || attemptFact.StreamCompletion != measurement.StreamIncomplete {
		t.Fatalf("incomplete stream entered successful measurements: request=%#v attempt=%#v", requestFact, attemptFact)
	}
}

func TestExecuteAuthenticityRunWithoutReferenceSavesInsufficientAssessment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	supplierID := uuid.MustParse("35000000-0000-0000-0000-000000000001")
	runID := uuid.MustParse("35000000-0000-0000-0000-000000000002")
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	store.runs[runID] = testRun(runID, supplierID, PurposeAuthenticity, now)
	prober := &fakeProber{}
	service := newTestService(store, prober, &fakeDispatcher{}, &fakeClock{now: now})

	if err := service.ExecuteRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if len(prober.requests) != 96 || len(store.samples[runID]) != 96 {
		t.Fatalf("probes=%d samples=%d, want 96", len(prober.requests), len(store.samples[runID]))
	}
	if len(store.authenticity) != 1 {
		t.Fatalf("saved %d authenticity assessments", len(store.authenticity))
	}
	assessment := store.authenticity[0].assessment
	if assessment.Verdict != domainevaluation.VerdictInsufficient || assessment.Reason != domainevaluation.ReasonNoTrustedReference {
		t.Fatalf("unexpected assessment: %#v", assessment)
	}
	if store.runs[runID].Status != RunSucceeded {
		t.Fatalf("run status is %q", store.runs[runID].Status)
	}
}

func TestExecuteRunRetryDoesNotRepeatAnUncertainPaidProbe(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	supplierID := uuid.MustParse("36000000-0000-0000-0000-000000000001")
	runID := uuid.MustParse("36000000-0000-0000-0000-000000000002")
	store.targets = []TargetAccess{testTarget(supplierID, "model-a")}
	store.runs[runID] = testRun(runID, supplierID, PurposeAuthenticity, now)
	store.failSaveSampleAt = 6
	prober := &fakeProber{}
	service := newTestService(store, prober, &fakeDispatcher{}, &fakeClock{now: now})

	firstErr := service.ExecuteRun(context.Background(), runID)
	if firstErr == nil || len(store.samples[runID]) != 6 || len(prober.requests) != 6 {
		t.Fatalf("first attempt: err=%v samples=%d probes=%d", firstErr, len(store.samples[runID]), len(prober.requests))
	}
	store.failSaveSampleAt = 0
	if err := service.ExecuteRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if len(store.samples[runID]) != 96 {
		t.Fatalf("saved %d samples, want 96", len(store.samples[runID]))
	}
	if len(prober.requests) != 96 {
		t.Fatalf("made %d probes, want 96; an uncertain paid probe was repeated", len(prober.requests))
	}
	uncertain := 0
	for _, sample := range store.samples[runID] {
		if sample.Outcome == "uncertain" {
			uncertain++
		}
	}
	if uncertain != 1 {
		t.Fatalf("uncertain sample count = %d, want 1", uncertain)
	}
	if store.runs[runID].Status != RunFailed || store.runs[runID].NextRetryAt != nil {
		t.Fatalf("run status is %q", store.runs[runID].Status)
	}
	if len(store.authenticity) != 0 {
		t.Fatal("uncertain run produced an authenticity conclusion")
	}
}

func TestCapabilityAssessmentUsesAnswerDigestsOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := newFakeStore()
	service := newTestService(store, &fakeProber{}, &fakeDispatcher{}, &fakeClock{now: now})
	run := testRun(uuid.MustParse("37000000-0000-0000-0000-000000000001"), uuid.MustParse("37000000-0000-0000-0000-000000000002"), PurposeQuality, now)
	samples := []Sample{
		{ProbeKey: "arithmetic", SampleIndex: 0, Outcome: "succeeded", NormalizedAnswer: "wrong", AnswerDigest: digestOf("95")},
		{ProbeKey: "ordering", SampleIndex: 0, Outcome: "succeeded", NormalizedAnswer: "2,5,9", AnswerDigest: digestOf("wrong")},
		{ProbeKey: "unicode", SampleIndex: 0, Outcome: "failed", AnswerDigest: digestOf("路由正常")},
		{ProbeKey: "json", SampleIndex: 0, Outcome: "succeeded", AnswerDigest: digestOf("{\"ok\":true,\"count\":3}")},
	}

	if err := service.finishCapability(context.Background(), run, samples); err != nil {
		t.Fatal(err)
	}
	if len(store.capabilities) != 1 {
		t.Fatalf("saved %d capability assessments", len(store.capabilities))
	}
	assessment := store.capabilities[0]
	if assessment.score != 50 || assessment.confidence != 0.75 || assessment.completed != 3 || assessment.planned != 4 {
		t.Fatalf("unexpected capability assessment: %#v", assessment)
	}
}

func TestResponseModelConflictDowngradesConsistentAssessment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	targetSupplierID := uuid.MustParse("38000000-0000-0000-0000-000000000001")
	referenceSupplierID := uuid.MustParse("38000000-0000-0000-0000-000000000002")
	runID := uuid.MustParse("38000000-0000-0000-0000-000000000003")
	referenceRunID := uuid.MustParse("38000000-0000-0000-0000-000000000004")
	referenceID := uuid.MustParse("38000000-0000-0000-0000-000000000005")
	run := testRun(runID, targetSupplierID, PurposeAuthenticity, now)
	run.ReferenceID = &referenceID
	referenceRun := testRun(referenceRunID, referenceSupplierID, PurposeAuthenticity, now.Add(-time.Hour))
	targetSamples := matchingAuthenticitySamples(run, run.UpstreamModel)
	referenceSamples := matchingAuthenticitySamples(referenceRun, referenceRun.UpstreamModel)
	targetSamples[0].ResponseModel = "different-model"
	referenceFingerprint, err := buildFingerprint(referenceRun, referenceSamples, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reference := domainevaluation.ModelReference{
		ID: referenceID.String(), Revision: 1, Trust: domainevaluation.ReferenceOfficial,
		Source:    domainevaluation.ModelSubject{SupplierID: referenceSupplierID, Model: referenceRun.Model},
		ExpiresAt: now.Add(time.Hour), Fingerprint: referenceFingerprint,
	}
	store := newFakeStore()
	store.referencesByID[referenceID] = reference
	service := newTestService(store, &fakeProber{}, &fakeDispatcher{}, &fakeClock{now: now})

	if err := service.finishAuthenticity(context.Background(), run, targetSamples); err != nil {
		t.Fatal(err)
	}
	if len(store.authenticity) != 1 {
		t.Fatalf("saved %d authenticity assessments", len(store.authenticity))
	}
	saved := store.authenticity[0]
	if !saved.responseConflict || saved.assessment.Verdict != domainevaluation.VerdictSuspicious || saved.assessment.Confidence != domainevaluation.ConfidenceLow {
		t.Fatalf("response model conflict did not downgrade assessment: %#v", saved)
	}
}

func TestResponseModelConflictIgnoresFailedSamples(t *testing.T) {
	t.Parallel()
	samples := []Sample{
		{Outcome: "failed", ResponseModel: "different-model"},
		{Outcome: "succeeded", ResponseModel: "model-a"},
	}
	if responseModelConflict("model-a", samples) {
		t.Fatal("failed sample changed response-model evidence")
	}
	samples = append(samples, Sample{Outcome: "succeeded", ResponseModel: "different-model"})
	if !responseModelConflict("model-a", samples) {
		t.Fatal("successful conflicting response model was ignored")
	}
}

func matchingAuthenticitySamples(run Run, responseModel string) []Sample {
	samples := make([]Sample, 0, plannedSamples(PurposeAuthenticity))
	for _, cell := range domainevaluation.RequiredCells() {
		answer := "7"
		switch cell {
		case domainevaluation.CellColorEN, domainevaluation.CellColorZH:
			answer = "blue"
		case domainevaluation.CellCoinEN, domainevaluation.CellCoinZH:
			answer = "heads"
		}
		for index := 0; index < authenticitySamplesPerCell; index++ {
			samples = append(samples, Sample{
				RunID: run.ID, ProbeKey: string(cell), SampleIndex: index,
				Outcome: "succeeded", NormalizedAnswer: answer,
				AnswerDigest: digestOf(answer), ResponseModel: responseModel,
				HTTPStatus: 200, CollectedAt: run.RequestedAt,
			})
		}
	}
	return samples
}
