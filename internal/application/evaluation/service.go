package evaluation

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/google/uuid"
)

const (
	authenticitySamplesPerCell  = 12
	healthRefreshInterval       = 6 * time.Hour
	qualityRefreshInterval      = 7 * 24 * time.Hour
	authenticityRefreshInterval = 7 * 24 * time.Hour
	runningRecoveryInterval     = 3*time.Hour + 15*time.Minute
	dailySampleBudget           = 200
)

var (
	evaluationRequestKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
	evaluationRequestHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Service struct {
	store      Store
	vault      Vault
	prober     Prober
	dispatcher Dispatcher
	now        func() time.Time
	newID      func() uuid.UUID
	newSeed    func() (uint64, error)
}

func NewService(
	store Store,
	vault Vault,
	prober Prober,
	dispatcher Dispatcher,
	now func() time.Time,
	newID func() uuid.UUID,
) (*Service, error) {
	if store == nil || vault == nil || prober == nil || dispatcher == nil || now == nil || newID == nil {
		return nil, errors.New("evaluation dependencies are required")
	}
	return &Service{
		store: store, vault: vault, prober: prober, dispatcher: dispatcher,
		now: now, newID: newID, newSeed: secureSeed,
	}, nil
}

func secureSeed() (uint64, error) {
	var value [8]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return 0, errors.New("create evaluation seed")
	}
	return binary.BigEndian.Uint64(value[:]), nil
}

func (service *Service) RequestRun(ctx context.Context, command RunCommand) (Run, error) {
	if command.SupplierID == uuid.Nil || strings.TrimSpace(command.Model) == "" || len(command.Model) > 191 {
		return Run{}, invalidEvaluation("测评目标无效")
	}
	if command.TargetKind == "" {
		command.TargetKind = TargetSupplierDirect
	}
	if command.TargetKind != TargetSupplierDirect {
		return Run{}, invalidEvaluation("站点链路测评需要专用探测密钥")
	}
	if !validPurpose(command.Purpose) {
		return Run{}, invalidEvaluation("测评用途无效")
	}
	command.Actor = strings.TrimSpace(command.Actor)
	command.Reason = strings.TrimSpace(command.Reason)
	if command.Actor == "" || command.Reason == "" || len(command.Reason) > 500 {
		return Run{}, invalidEvaluation("测评原因无效")
	}
	if !validRequestIdentity(command.RequestKey, command.RequestHash) {
		return Run{}, invalidEvaluation("测评请求编号无效")
	}
	if command.RequestKey != "" {
		existing, err := service.store.FindEvaluationRunByRequestKey(ctx, command.RequestKey)
		if err != nil {
			return Run{}, err
		}
		if existing != nil {
			if existing.RequestHash != command.RequestHash {
				return Run{}, ErrRequestKeyReused
			}
			return service.retryExistingRunIfDue(ctx, *existing, service.now().UTC())
		}
	}
	target, err := service.store.GetEvaluationTarget(ctx, command.SupplierID, command.Model)
	if err != nil {
		return Run{}, err
	}
	now := service.now().UTC()
	existing, err := service.store.FindRecentEvaluationRun(
		ctx, target.SupplierID, target.Model, command.TargetKind, command.Purpose, time.Unix(0, 0).UTC(),
	)
	if err != nil {
		return Run{}, err
	}
	if existing != nil && (existing.Status == RunPending || existing.Status == RunRunning || existing.Status == RunUncertain) {
		return service.retryExistingRunIfDue(ctx, *existing, now)
	}
	planned := plannedSamples(command.Purpose)
	seed, err := service.newSeed()
	if err != nil {
		return Run{}, err
	}
	reference, err := service.store.FindTrustedReference(ctx, target.Model, now)
	if err != nil {
		return Run{}, err
	}
	var referenceID *uuid.UUID
	if reference != nil && reference.Source.SupplierID != target.SupplierID {
		parsed, parseErr := uuid.Parse(reference.ID)
		if parseErr != nil {
			return Run{}, errors.New("trusted reference identity is invalid")
		}
		referenceID = &parsed
	}
	run := Run{
		ID: service.newID(), SupplierID: target.SupplierID, SupplierName: target.SupplierName,
		Model: target.Model, UpstreamModel: target.UpstreamModel, TargetKind: command.TargetKind,
		Purpose: command.Purpose, Status: RunPending, SuiteVersion: suiteVersion(command.Purpose),
		AlgorithmVersion: AlgorithmVersion, Seed: seed, ReferenceID: referenceID,
		PlannedSamples: planned, RequestedBy: command.Actor, RequestReason: command.Reason, RequestedAt: now,
		RequestKey: command.RequestKey, RequestHash: command.RequestHash,
	}
	run, err = service.store.CreateEvaluationRun(ctx, run, dailySampleBudget)
	if err != nil {
		return Run{}, err
	}
	return service.dispatchRun(ctx, run, now)
}

func (service *Service) GetRun(ctx context.Context, runID uuid.UUID) (Run, error) {
	if runID == uuid.Nil {
		return Run{}, invalidEvaluation("测评运行编号无效")
	}
	return service.store.GetEvaluationRun(ctx, runID)
}

func (service *Service) ListRuns(ctx context.Context, filter RunFilter) (RunPage, error) {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return RunPage{}, invalidEvaluation("测评分页参数无效")
	}
	filter.Model = strings.TrimSpace(filter.Model)
	if filter.Purpose != "" && !validPurpose(filter.Purpose) {
		return RunPage{}, invalidEvaluation("测评用途筛选无效")
	}
	return service.store.ListEvaluationRuns(ctx, filter)
}

func (service *Service) ScheduleDue(ctx context.Context) error {
	targets, err := service.store.ListEvaluationTargets(ctx)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	var failures []error
	for _, target := range targets {
		if err := service.schedulePurposeIfDue(ctx, target, PurposeHealth, healthRefreshInterval, now); err != nil {
			failures = append(failures, err)
		}
		if err := service.schedulePurposeIfDue(ctx, target, PurposeQuality, qualityRefreshInterval, now); err != nil {
			failures = append(failures, err)
		}
		reference, referenceErr := service.store.FindTrustedReference(ctx, target.Model, now)
		if referenceErr != nil {
			failures = append(failures, referenceErr)
			continue
		}
		if reference != nil && reference.Source.SupplierID != target.SupplierID {
			if err := service.schedulePurposeIfDue(ctx, target, PurposeAuthenticity, authenticityRefreshInterval, now); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (service *Service) schedulePurposeIfDue(ctx context.Context, target TargetAccess, purpose Purpose, interval time.Duration, now time.Time) error {
	recent, err := service.store.FindRecentEvaluationRun(ctx, target.SupplierID, target.Model, TargetSupplierDirect, purpose, now.Add(-interval))
	if err != nil {
		return err
	}
	if recent != nil {
		_, err = service.retryExistingRunIfDue(ctx, *recent, now)
		return err
	}
	_, err = service.RequestRun(ctx, RunCommand{
		SupplierID: target.SupplierID, Model: target.Model, Purpose: purpose,
		TargetKind: TargetSupplierDirect, Reason: "周期性低频测评", Actor: "system",
	})
	return err
}

func (service *Service) retryExistingRunIfDue(ctx context.Context, run Run, now time.Time) (Run, error) {
	if run.Status == RunPending {
		return service.dispatchRun(ctx, run, now)
	}
	if run.Status == RunRunning && (run.StartedAt == nil || !run.StartedAt.Add(runningRecoveryInterval).After(now)) {
		return service.dispatchRun(ctx, run, now)
	}
	if run.Status != RunUncertain || run.NextRetryAt == nil || run.NextRetryAt.After(now) {
		return run, nil
	}
	return service.dispatchRun(ctx, run, now)
}

func (service *Service) dispatchRun(ctx context.Context, run Run, now time.Time) (Run, error) {
	if err := service.dispatcher.DispatchEvaluation(ctx, run.ID); err != nil {
		retryAt := now.Add(time.Minute)
		const code = "dispatch_failed"
		const message = "测评任务暂未进入队列"
		persistErr := service.store.FailEvaluationRun(ctx, run.ID, RunUncertain, code, message, &retryAt, now)
		run.Status = RunUncertain
		run.ErrorCode = code
		run.ErrorMessage = message
		run.CompletedAt = &now
		run.NextRetryAt = &retryAt
		return run, errors.Join(err, persistErr)
	}
	return run, nil
}

func (service *Service) PromoteReference(ctx context.Context, command ReferenceCommand) (domainevaluation.ModelReference, error) {
	if command.RunID == uuid.Nil || (command.Trust != domainevaluation.ReferenceOfficial && command.Trust != domainevaluation.ReferenceOperatorTrusted && command.Trust != domainevaluation.ReferenceCommunity) {
		return domainevaluation.ModelReference{}, invalidEvaluation("可信参考请求无效")
	}
	command.Reason = strings.TrimSpace(command.Reason)
	command.Actor = strings.TrimSpace(command.Actor)
	if command.Reason == "" || command.Actor == "" || len(command.Reason) > 500 {
		return domainevaluation.ModelReference{}, invalidEvaluation("必须填写可信参考原因")
	}
	if command.ValidDays < 1 || command.ValidDays > 90 {
		return domainevaluation.ModelReference{}, invalidEvaluation("可信参考有效期必须为 1 至 90 天")
	}
	if !validRequestIdentity(command.RequestKey, command.RequestHash) {
		return domainevaluation.ModelReference{}, invalidEvaluation("可信参考请求编号无效")
	}
	run, err := service.store.GetEvaluationRun(ctx, command.RunID)
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	if run.Status != RunSucceeded || run.Purpose != PurposeAuthenticity || run.TargetKind != TargetSupplierDirect {
		return domainevaluation.ModelReference{}, invalidEvaluation("只有完成的供应商直连真实性测评可以成为参考")
	}
	fingerprint, err := service.store.GetEvaluationFingerprint(ctx, run.ID)
	if err != nil {
		return domainevaluation.ModelReference{}, err
	}
	if !fingerprint.Stability.Measured || math.IsNaN(fingerprint.Stability.Distance) || math.IsInf(fingerprint.Stability.Distance, 0) || fingerprint.Stability.Distance < 0 || fingerprint.Stability.Distance > domainevaluation.DefaultAuthenticityPolicy().MaximumSelfDistance {
		return domainevaluation.ModelReference{}, invalidEvaluation("不稳定的模型指纹不能成为可信参考")
	}
	now := service.now().UTC()
	return service.store.CreateTrustedReference(
		ctx, service.newID(), run, command.Trust, command.Reason, command.Actor,
		now, now.AddDate(0, 0, command.ValidDays), command.RequestKey, command.RequestHash,
	)
}

func validRequestIdentity(key, hash string) bool {
	if key == "" && hash == "" {
		return true
	}
	return evaluationRequestKeyPattern.MatchString(key) && evaluationRequestHashPattern.MatchString(hash)
}

func validPurpose(purpose Purpose) bool {
	return purpose == PurposeHealth || purpose == PurposeAuthenticity || purpose == PurposeQuality || purpose == PurposeRecovery
}

func plannedSamples(purpose Purpose) int {
	switch purpose {
	case PurposeAuthenticity:
		return len(domainevaluation.RequiredCells()) * authenticitySamplesPerCell
	case PurposeQuality:
		return 4
	case PurposeRecovery:
		return 3
	default:
		return 1
	}
}

func suiteVersion(purpose Purpose) string {
	switch purpose {
	case PurposeAuthenticity:
		return FingerprintSuiteVersion
	case PurposeQuality:
		return CapabilitySuiteVersion
	default:
		return HealthSuiteVersion
	}
}

func (service *Service) failRun(ctx context.Context, run Run, code, message string, cause error) error {
	now := service.now().UTC()
	retryAt := now.Add(time.Minute)
	if err := service.store.FailEvaluationRun(ctx, run.ID, RunUncertain, code, message, &retryAt, now); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func invalidRunError(runID uuid.UUID) error {
	return fmt.Errorf("evaluation run %s is invalid", runID)
}

func invalidEvaluation(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
