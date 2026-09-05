package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

type savedAuthenticity struct {
	runID            uuid.UUID
	subject          domainevaluation.ModelSubject
	referenceID      *uuid.UUID
	assessment       domainevaluation.AuthenticityAssessment
	responseConflict bool
	at               time.Time
}

type savedCapability struct {
	runID      uuid.UUID
	subject    domainevaluation.ModelSubject
	score      float64
	confidence float64
	completed  int
	planned    int
	suite      string
	at         time.Time
}

type fakeStore struct {
	targets              []TargetAccess
	runs                 map[uuid.UUID]Run
	createdRuns          []Run
	samples              map[uuid.UUID][]Sample
	fingerprints         map[uuid.UUID]domainevaluation.Fingerprint
	referencesByModel    map[string]*domainevaluation.ModelReference
	referencesByID       map[uuid.UUID]domainevaluation.ModelReference
	priorMismatch        *domainevaluation.MismatchEvidence
	authenticity         []savedAuthenticity
	capabilities         []savedCapability
	dailyCount           int64
	dailyLimits          []int
	saveSampleAttempts   int
	failSaveSampleAt     int
	createReferenceCalls int
	failures             []RunStatus
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		runs:              make(map[uuid.UUID]Run),
		samples:           make(map[uuid.UUID][]Sample),
		fingerprints:      make(map[uuid.UUID]domainevaluation.Fingerprint),
		referencesByModel: make(map[string]*domainevaluation.ModelReference),
		referencesByID:    make(map[uuid.UUID]domainevaluation.ModelReference),
	}
}

func (store *fakeStore) ListEvaluationTargets(context.Context) ([]TargetAccess, error) {
	return append([]TargetAccess(nil), store.targets...), nil
}

func (store *fakeStore) GetEvaluationTarget(_ context.Context, supplierID uuid.UUID, model string) (TargetAccess, error) {
	for _, target := range store.targets {
		if target.SupplierID == supplierID && target.Model == model {
			return target, nil
		}
	}
	return TargetAccess{}, errors.New("evaluation target not found")
}

func (store *fakeStore) CreateEvaluationRun(_ context.Context, run Run, dailyLimit int) (Run, error) {
	store.dailyLimits = append(store.dailyLimits, dailyLimit)
	if store.dailyCount+int64(run.PlannedSamples) > int64(dailyLimit) {
		return Run{}, errors.New("evaluation daily request budget is exhausted")
	}
	store.dailyCount += int64(run.PlannedSamples)
	store.runs[run.ID] = run
	store.createdRuns = append(store.createdRuns, run)
	return run, nil
}

func (store *fakeStore) FindEvaluationRunByRequestKey(_ context.Context, key string) (*Run, error) {
	for _, run := range store.runs {
		if run.RequestKey == key {
			copy := run
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *fakeStore) GetEvaluationRun(_ context.Context, id uuid.UUID) (Run, error) {
	run, ok := store.runs[id]
	if !ok {
		return Run{}, errors.New("evaluation run not found")
	}
	return run, nil
}

func (store *fakeStore) ListEvaluationRuns(context.Context, RunFilter) (RunPage, error) {
	items := append([]Run(nil), store.createdRuns...)
	return RunPage{Items: items, Total: int64(len(items)), Limit: len(items)}, nil
}

func (store *fakeStore) FindRecentEvaluationRun(_ context.Context, supplierID uuid.UUID, model string, targetKind TargetKind, purpose Purpose, since time.Time) (*Run, error) {
	for index := len(store.createdRuns) - 1; index >= 0; index-- {
		run := store.createdRuns[index]
		if current, ok := store.runs[run.ID]; ok {
			run = current
		}
		if run.SupplierID == supplierID && run.Model == model && run.TargetKind == targetKind && run.Purpose == purpose && !run.RequestedAt.Before(since) {
			copy := run
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *fakeStore) StartEvaluationRun(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	run, ok := store.runs[id]
	if !ok {
		return false, errors.New("evaluation run not found")
	}
	run.Status = RunRunning
	run.StartedAt = &at
	store.runs[id] = run
	return true, nil
}

func (store *fakeStore) ListEvaluationSamples(_ context.Context, runID uuid.UUID) ([]Sample, error) {
	return append([]Sample(nil), store.samples[runID]...), nil
}

func (store *fakeStore) ReserveEvaluationSample(_ context.Context, sample Sample) (bool, error) {
	identity := sampleIdentity(sample.ProbeKey, sample.SampleIndex)
	for _, existing := range store.samples[sample.RunID] {
		if sampleIdentity(existing.ProbeKey, existing.SampleIndex) == identity {
			return false, nil
		}
	}
	store.samples[sample.RunID] = append(store.samples[sample.RunID], sample)
	return true, nil
}

func (store *fakeStore) CompleteEvaluationSample(_ context.Context, sample Sample, _ measurement.RequestFact, _ measurement.AttemptFact) error {
	store.saveSampleAttempts++
	if store.failSaveSampleAt > 0 && store.saveSampleAttempts == store.failSaveSampleAt {
		return errors.New("save sample failed")
	}
	identity := sampleIdentity(sample.ProbeKey, sample.SampleIndex)
	for index, existing := range store.samples[sample.RunID] {
		if sampleIdentity(existing.ProbeKey, existing.SampleIndex) == identity {
			store.samples[sample.RunID][index] = sample
			run := store.runs[sample.RunID]
			run.CompletedSamples++
			store.runs[sample.RunID] = run
			return nil
		}
	}
	return errors.New("sample reservation not found")
}

func (store *fakeStore) SaveEvaluationFingerprint(_ context.Context, fingerprint domainevaluation.Fingerprint) error {
	runID, err := uuid.Parse(fingerprint.RunID)
	if err != nil {
		return errors.New("fingerprint run identity is invalid")
	}
	store.fingerprints[runID] = fingerprint
	return nil
}

func (store *fakeStore) GetEvaluationFingerprint(_ context.Context, runID uuid.UUID) (domainevaluation.Fingerprint, error) {
	fingerprint, ok := store.fingerprints[runID]
	if !ok {
		return domainevaluation.Fingerprint{}, errors.New("fingerprint not found")
	}
	return fingerprint, nil
}

func (store *fakeStore) FindTrustedReference(_ context.Context, model string, _ time.Time) (*domainevaluation.ModelReference, error) {
	reference := store.referencesByModel[model]
	if reference == nil {
		return nil, nil
	}
	copy := *reference
	return &copy, nil
}

func (store *fakeStore) GetTrustedReference(_ context.Context, id uuid.UUID) (domainevaluation.ModelReference, error) {
	reference, ok := store.referencesByID[id]
	if !ok {
		return domainevaluation.ModelReference{}, errors.New("reference not found")
	}
	return reference, nil
}

func (store *fakeStore) FindPreviousMismatch(_ context.Context, _ domainevaluation.ModelSubject, _ domainevaluation.ModelReference, _ float64, _ time.Time) (*domainevaluation.MismatchEvidence, error) {
	if store.priorMismatch == nil {
		return nil, nil
	}
	copy := *store.priorMismatch
	return &copy, nil
}

func (store *fakeStore) SaveAuthenticityAssessment(_ context.Context, runID uuid.UUID, subject domainevaluation.ModelSubject, referenceID *uuid.UUID, assessment domainevaluation.AuthenticityAssessment, responseConflict bool, at time.Time) error {
	store.authenticity = append(store.authenticity, savedAuthenticity{
		runID: runID, subject: subject, referenceID: referenceID,
		assessment: assessment, responseConflict: responseConflict, at: at,
	})
	return nil
}

func (store *fakeStore) SaveCapabilityAssessment(_ context.Context, runID uuid.UUID, subject domainevaluation.ModelSubject, score, confidence float64, completed, planned int, suite string, at time.Time) error {
	store.capabilities = append(store.capabilities, savedCapability{
		runID: runID, subject: subject, score: score, confidence: confidence,
		completed: completed, planned: planned, suite: suite, at: at,
	})
	return nil
}

func (store *fakeStore) CompleteEvaluationRun(_ context.Context, id uuid.UUID, at time.Time) error {
	run := store.runs[id]
	run.Status = RunSucceeded
	run.CompletedAt = &at
	store.runs[id] = run
	return nil
}

func (store *fakeStore) FailEvaluationRun(_ context.Context, id uuid.UUID, status RunStatus, code, message string, retryAt *time.Time, at time.Time) error {
	run := store.runs[id]
	run.Status = status
	run.ErrorCode = code
	run.ErrorMessage = message
	run.CompletedAt = &at
	if retryAt == nil {
		run.NextRetryAt = nil
	} else {
		copy := *retryAt
		run.NextRetryAt = &copy
	}
	store.runs[id] = run
	store.failures = append(store.failures, status)
	return nil
}

func (store *fakeStore) CreateTrustedReference(_ context.Context, id uuid.UUID, run Run, trust domainevaluation.ReferenceTrust, _, _ string, _ time.Time, expiresAt time.Time, _, _ string) (domainevaluation.ModelReference, error) {
	store.createReferenceCalls++
	fingerprint, ok := store.fingerprints[run.ID]
	if !ok {
		return domainevaluation.ModelReference{}, errors.New("fingerprint not found")
	}
	reference := domainevaluation.ModelReference{
		ID: id.String(), Revision: 1, Trust: trust,
		Source:    domainevaluation.ModelSubject{SupplierID: run.SupplierID, Model: run.Model},
		ExpiresAt: expiresAt, Fingerprint: fingerprint,
	}
	store.referencesByModel[run.Model] = &reference
	store.referencesByID[id] = reference
	return reference, nil
}

type fakeVault struct {
	secret []byte
	err    error
}

func (vault *fakeVault) Decrypt(credential.Record) ([]byte, error) {
	if vault.err != nil {
		return nil, vault.err
	}
	return append([]byte(nil), vault.secret...), nil
}

type fakeProber struct {
	requests []ProbeRequest
	probe    func(ProbeRequest) (ProbeResult, error)
}

func (prober *fakeProber) Probe(_ context.Context, _ string, _ []byte, request ProbeRequest) (ProbeResult, error) {
	prober.requests = append(prober.requests, request)
	if prober.probe != nil {
		return prober.probe(request)
	}
	return ProbeResult{
		Text: answerForPrompt(request.Prompt), ResponseModel: request.Model,
		HTTPStatus: 200, FinishReason: "stop", InputTokens: 10,
		OutputTokens: 1, TotalMillis: 10, StreamCompleted: true,
	}, nil
}

func answerForPrompt(prompt string) string {
	switch {
	case strings.Contains(prompt, "color"):
		return "blue"
	case strings.Contains(prompt, "颜色"):
		return "蓝色"
	case strings.Contains(prompt, "coin"), strings.Contains(prompt, "heads or tails"):
		return "heads"
	case strings.Contains(prompt, "硬币"):
		return "正面"
	default:
		return "7"
	}
}

type fakeDispatcher struct {
	runIDs []uuid.UUID
	err    error
}

func (dispatcher *fakeDispatcher) DispatchEvaluation(_ context.Context, runID uuid.UUID) error {
	dispatcher.runIDs = append(dispatcher.runIDs, runID)
	return dispatcher.err
}

type fakeClock struct {
	now time.Time
}

func newTestService(store *fakeStore, prober *fakeProber, dispatcher *fakeDispatcher, clock *fakeClock) *Service {
	sequence := 0
	service, err := NewService(
		store,
		&fakeVault{secret: []byte("supplier-key")},
		prober,
		dispatcher,
		func() time.Time { return clock.now },
		func() uuid.UUID {
			sequence++
			return uuid.NewSHA1(uuid.Nil, []byte(strconv.Itoa(sequence)))
		},
	)
	if err != nil {
		panic(err)
	}
	service.newSeed = func() (uint64, error) { return 42, nil }
	return service
}

func testTarget(id uuid.UUID, model string) TargetAccess {
	return TargetAccess{
		SupplierID: id, SupplierName: "supplier", BaseURL: "https://supplier.example",
		Model: model, UpstreamModel: model,
	}
}

func testRun(id, supplierID uuid.UUID, purpose Purpose, now time.Time) Run {
	return Run{
		ID: id, SupplierID: supplierID, Model: "model-a", UpstreamModel: "model-a",
		TargetKind: TargetSupplierDirect, Purpose: purpose, Status: RunPending,
		SuiteVersion: suiteVersion(purpose), AlgorithmVersion: AlgorithmVersion,
		Seed: 42, PlannedSamples: plannedSamples(purpose), RequestedAt: now,
	}
}

func digestOf(value string) string {
	// Keep test digest creation separate from production helpers so the test
	// verifies the persisted contract instead of calling the code under test.
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
