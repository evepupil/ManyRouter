package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

type plannedProbe struct {
	key         string
	cell        domainevaluation.CellID
	index       int
	variant     int
	prompt      string
	expected    string
	stream      bool
	temperature float64
	topP        float64
	maxTokens   int
}

func (service *Service) ExecuteRun(ctx context.Context, runID uuid.UUID) error {
	run, err := service.store.GetEvaluationRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.ID == uuid.Nil {
		return invalidRunError(runID)
	}
	if run.Status == RunSucceeded || run.Status == RunFailed || run.Status == RunCancelled {
		return nil
	}
	now := service.now().UTC()
	if run.Status != RunRunning {
		started, startErr := service.store.StartEvaluationRun(ctx, run.ID, now)
		if startErr != nil {
			return startErr
		}
		if !started {
			return nil
		}
	}
	target, err := service.store.GetEvaluationTarget(ctx, run.SupplierID, run.Model)
	if err != nil {
		return service.failRun(ctx, run, "target_unavailable", "测评目标不可用", err)
	}
	secret, err := service.vault.Decrypt(target.Credential)
	if err != nil {
		return service.failRun(ctx, run, "credential_unavailable", "供应商测评凭证无法解密", err)
	}
	defer clear(secret)
	existing, err := service.store.ListEvaluationSamples(ctx, run.ID)
	if err != nil {
		return service.failRun(ctx, run, "sample_state_unavailable", "测评样本状态读取失败", err)
	}
	existingKeys := make(map[string]struct{}, len(existing))
	for _, sample := range existing {
		existingKeys[sampleIdentity(sample.ProbeKey, sample.SampleIndex)] = struct{}{}
	}
	plan, err := evaluationPlan(run)
	if err != nil {
		return service.failRun(ctx, run, "invalid_evaluation_plan", "测评计划无效", err)
	}
	for _, probe := range plan {
		if _, ok := existingKeys[sampleIdentity(probe.key, probe.index)]; ok {
			continue
		}
		reserved, reserveErr := service.store.ReserveEvaluationSample(ctx, Sample{
			RunID: run.ID, ProbeKey: probe.key, SampleIndex: probe.index, PromptVariant: probe.variant,
			Outcome: "uncertain", CollectedAt: service.now().UTC(),
		})
		if reserveErr != nil {
			return service.failRun(ctx, run, "sample_reservation_failed", "测评样本预留失败", reserveErr)
		}
		if !reserved {
			continue
		}
		sample, requestFact, attemptFact, probeErr := service.executeProbe(ctx, run, target, secret, probe)
		if probeErr != nil {
			return service.failRun(ctx, run, "sample_invalid", "测评结果无法形成安全记录", probeErr)
		}
		if err := service.store.CompleteEvaluationSample(ctx, sample, requestFact, attemptFact); err != nil {
			return service.failRun(ctx, run, "sample_commit_failed", "测评样本提交失败", err)
		}
	}
	samples, err := service.store.ListEvaluationSamples(ctx, run.ID)
	if err != nil {
		return service.failRun(ctx, run, "sample_reload_failed", "测评样本汇总失败", err)
	}
	if len(samples) != run.PlannedSamples {
		return service.failRun(ctx, run, "sample_count_incomplete", "测评样本数量不完整", errors.New("evaluation sample count is incomplete"))
	}
	for _, sample := range samples {
		if sample.Outcome == "uncertain" {
			now := service.now().UTC()
			return service.store.FailEvaluationRun(
				ctx, run.ID, RunFailed, "sample_outcome_uncertain", "测评样本结果待人工核对", nil, now,
			)
		}
	}
	if run.Purpose == PurposeAuthenticity {
		if err := service.finishAuthenticity(ctx, run, samples); err != nil {
			return service.failRun(ctx, run, "authenticity_assessment_failed", "真实性结论生成失败", err)
		}
	} else {
		if err := service.finishCapability(ctx, run, samples); err != nil {
			return service.failRun(ctx, run, "capability_assessment_failed", "能力结论生成失败", err)
		}
	}
	if err := service.store.CompleteEvaluationRun(ctx, run.ID, service.now().UTC()); err != nil {
		return err
	}
	return nil
}

func (service *Service) executeProbe(
	ctx context.Context,
	run Run,
	target TargetAccess,
	secret []byte,
	probe plannedProbe,
) (Sample, measurement.RequestFact, measurement.AttemptFact, error) {
	startedAt := service.now().UTC()
	result, probeErr := service.prober.Probe(ctx, target.BaseURL, secret, ProbeRequest{
		Model: target.UpstreamModel, Prompt: probe.prompt, Temperature: probe.temperature,
		TopP: probe.topP, MaxTokens: probe.maxTokens, Stream: probe.stream,
	})
	collectedAt := service.now().UTC()
	totalMillis := result.TotalMillis
	streamCompleted := (*bool)(nil)
	if probe.stream {
		value := result.StreamCompleted
		streamCompleted = &value
	}
	streamIncomplete := probe.stream && !result.StreamCompleted
	succeeded := probeErr == nil && result.HTTPStatus >= 200 && result.HTTPStatus < 300 && !streamIncomplete
	errorText := ""
	stableErrorCode := ""
	if probeErr != nil {
		errorText = "supplier probe protocol failed"
	} else if streamIncomplete {
		stableErrorCode = "stream_incomplete"
		errorText = "supplier probe stream incomplete"
	}
	classified := measurement.ErrorFact{}
	if !succeeded {
		classified = measurement.ClassifyError(stableErrorCode, result.HTTPStatus, errorText)
	}
	normalized := ""
	if succeeded && probe.cell != "" {
		normalized, _ = domainevaluation.NormalizeAnswer(probe.cell, result.Text)
	}
	answerDigest := ""
	if result.Text != "" {
		digest := sha256.Sum256([]byte(result.Text))
		answerDigest = hex.EncodeToString(digest[:])
	}
	outcome := "succeeded"
	if !succeeded {
		outcome = "failed"
	}
	sample := Sample{
		RunID: run.ID, ProbeKey: probe.key, SampleIndex: probe.index, PromptVariant: probe.variant,
		Outcome: outcome, NormalizedAnswer: normalized, AnswerDigest: answerDigest,
		ResponseModel: result.ResponseModel, HTTPStatus: result.HTTPStatus,
		FinishReason: result.FinishReason, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		FirstTokenMillis: result.FirstTokenMillis, TotalMillis: &totalMillis,
		Stream: probe.stream, StreamCompleted: streamCompleted, Error: classified, CollectedAt: collectedAt,
	}
	requestFact, attemptFact, factErr := measurement.NewProbeFacts(measurement.ProbeInput{
		RunID: run.ID, SampleKey: sampleIdentity(probe.key, probe.index), Source: measurement.SourceDirectProbe,
		SupplierID: run.SupplierID, Model: run.Model, Succeeded: succeeded, HTTPStatus: result.HTTPStatus,
		StableErrorCode: stableErrorCode, ErrorText: errorText, IsStream: probe.stream, StreamCompleted: streamCompleted,
		FirstTokenMillis: result.FirstTokenMillis, TotalMillis: &totalMillis,
		PromptTokens: result.InputTokens, CompletionTokens: result.OutputTokens, OccurredAt: startedAt,
	})
	if factErr != nil {
		return Sample{}, measurement.RequestFact{}, measurement.AttemptFact{}, factErr
	}
	return sample, requestFact, attemptFact, nil
}

func evaluationPlan(run Run) ([]plannedProbe, error) {
	random := rand.New(rand.NewSource(int64(run.Seed)))
	switch run.Purpose {
	case PurposeAuthenticity:
		result := make([]plannedProbe, 0, run.PlannedSamples)
		for _, cell := range domainevaluation.RequiredCells() {
			prompts := fingerprintPrompts(cell)
			if len(prompts) == 0 {
				return nil, errors.New("fingerprint cell has no prompts")
			}
			for index := 0; index < authenticitySamplesPerCell; index++ {
				variant := random.Intn(len(prompts))
				result = append(result, plannedProbe{
					key: string(cell), cell: cell, index: index, variant: variant, prompt: prompts[variant],
					temperature: 1, topP: 1, maxTokens: 16,
				})
			}
		}
		return result, nil
	case PurposeQuality:
		return objectiveCapabilityPlan(), nil
	case PurposeHealth, PurposeRecovery:
		count := plannedSamples(run.Purpose)
		result := make([]plannedProbe, 0, count)
		for index := 0; index < count; index++ {
			left, right := random.Intn(40)+10, random.Intn(40)+10
			expected := strconv.Itoa(left + right)
			result = append(result, plannedProbe{
				key: "stream_health", index: index, prompt: fmt.Sprintf("Reply with only the integer result of %d + %d.", left, right),
				expected: expected, stream: true, temperature: 0, topP: 1, maxTokens: 8,
			})
		}
		return result, nil
	default:
		return nil, errors.New("evaluation purpose is unsupported")
	}
}

func fingerprintPrompts(cell domainevaluation.CellID) []string {
	switch cell {
	case domainevaluation.CellNumber100EN:
		return []string{"Name a random whole number from 1 to 100. Reply only with the number.", "Choose one random integer between 1 and 100. Output only that integer."}
	case domainevaluation.CellNumber100ZH:
		return []string{"从1到100随机选一个整数，只回答数字。", "请随机给出1至100之间的一个整数，只输出该整数。"}
	case domainevaluation.CellNumber10EN:
		return []string{"Pick a random integer from 1 to 10. Reply only with the number.", "Choose one whole number between 1 and 10 at random. Output only it."}
	case domainevaluation.CellNumber10ZH:
		return []string{"从1到10随机选一个整数，只回答数字。", "请随机给出1至10之间的一个整数，只输出该整数。"}
	case domainevaluation.CellColorEN:
		return []string{"Name one random basic color. Reply with one color word only.", "Choose a random common color and output only its name."}
	case domainevaluation.CellColorZH:
		return []string{"随机说出一种常见颜色，只回答颜色名称。", "请选择一种随机的基础颜色，只输出名称。"}
	case domainevaluation.CellCoinEN:
		return []string{"Flip an imaginary fair coin. Reply only heads or tails.", "Choose heads or tails at random. Output one word only."}
	case domainevaluation.CellCoinZH:
		return []string{"模拟抛一次公平硬币，只回答正面或反面。", "随机选择硬币的正面或反面，只输出结果。"}
	default:
		return nil
	}
}

func objectiveCapabilityPlan() []plannedProbe {
	return []plannedProbe{
		{key: "arithmetic", prompt: "Reply only with the integer result of 37 + 58.", expected: "95", topP: 1, maxTokens: 16},
		{key: "ordering", prompt: "Sort 9, 2, and 5 ascending. Reply only as comma-separated digits.", expected: "2,5,9", topP: 1, maxTokens: 16},
		{key: "unicode", prompt: "Reply with exactly the four characters: 路由正常", expected: "路由正常", topP: 1, maxTokens: 16},
		{key: "json", prompt: "Reply only with this JSON object: {\"ok\":true,\"count\":3}", expected: "{\"ok\":true,\"count\":3}", topP: 1, maxTokens: 32},
	}
}

func sampleIdentity(key string, index int) string {
	return key + ":" + strconv.Itoa(index)
}

func (service *Service) finishCapability(ctx context.Context, run Run, samples []Sample) error {
	plan, err := evaluationPlan(run)
	if err != nil {
		return err
	}
	expected := make(map[string]string, len(plan))
	for _, probe := range plan {
		expected[sampleIdentity(probe.key, probe.index)] = probe.expected
	}
	correct := 0
	completed := 0
	for _, sample := range samples {
		if sample.Outcome != "succeeded" {
			continue
		}
		completed++
		if normalizeObjectiveAnswer(sample, expected[sampleIdentity(sample.ProbeKey, sample.SampleIndex)]) {
			correct++
		}
	}
	score := 0.0
	if len(plan) > 0 {
		score = float64(correct) * 100 / float64(len(plan))
	}
	confidence := float64(completed) / float64(len(plan))
	return service.store.SaveCapabilityAssessment(
		ctx, run.ID, domainevaluation.ModelSubject{SupplierID: run.SupplierID, Model: run.Model},
		score, confidence, completed, len(plan), run.SuiteVersion, service.now().UTC(),
	)
}

func normalizeObjectiveAnswer(sample Sample, expected string) bool {
	if expected == "" || sample.AnswerDigest == "" {
		return false
	}
	// Objective runs store only an answer digest. Recreate the expected digest so
	// the private response never needs to be persisted.
	digest := sha256.Sum256([]byte(expected))
	return sample.AnswerDigest == hex.EncodeToString(digest[:])
}

func (service *Service) finishAuthenticity(ctx context.Context, run Run, samples []Sample) error {
	fingerprint, err := buildFingerprint(run, samples, service.now().UTC())
	if err != nil {
		return err
	}
	if err := service.store.SaveEvaluationFingerprint(ctx, fingerprint); err != nil {
		return err
	}
	var reference *domainevaluation.ModelReference
	if run.ReferenceID != nil {
		loaded, loadErr := service.store.GetTrustedReference(ctx, *run.ReferenceID)
		if loadErr != nil {
			return loadErr
		}
		reference = &loaded
	}
	policy := domainevaluation.DefaultAuthenticityPolicy()
	var prior *domainevaluation.MismatchEvidence
	if reference != nil {
		prior, err = service.store.FindPreviousMismatch(
			ctx, domainevaluation.ModelSubject{SupplierID: run.SupplierID, Model: run.Model},
			*reference, policy.UncertainMaximum, fingerprint.CollectedAt.Add(-policy.MismatchConfirmationDelay),
		)
		if err != nil {
			return err
		}
	}
	assessment, err := domainevaluation.AssessAuthenticity(domainevaluation.AssessmentInput{
		Subject: domainevaluation.ModelSubject{SupplierID: run.SupplierID, Model: run.Model},
		Target:  fingerprint, Reference: reference, PriorMismatch: prior, AssessedAt: fingerprint.CollectedAt,
	}, policy)
	if err != nil {
		return err
	}
	responseConflict := responseModelConflict(run.UpstreamModel, samples)
	if responseConflict && assessment.Verdict == domainevaluation.VerdictConsistent {
		assessment.Verdict = domainevaluation.VerdictSuspicious
		assessment.Confidence = domainevaluation.ConfidenceLow
		assessment.Reason = domainevaluation.ReasonDistanceUncertain
	}
	return service.store.SaveAuthenticityAssessment(
		ctx, run.ID, domainevaluation.ModelSubject{SupplierID: run.SupplierID, Model: run.Model},
		run.ReferenceID, assessment, responseConflict, fingerprint.CollectedAt,
	)
}

func buildFingerprint(run Run, samples []Sample, collectedAt time.Time) (domainevaluation.Fingerprint, error) {
	fingerprint := domainevaluation.Fingerprint{
		RunID: run.ID.String(), Seed: run.Seed, ProtocolVersion: domainevaluation.ProtocolSingleTokenJSDV1,
		CollectedAt: collectedAt, Cells: make(map[domainevaluation.CellID]domainevaluation.Distribution),
	}
	var stabilitySum float64
	stableCells := 0
	for _, cell := range domainevaluation.RequiredCells() {
		all := domainevaluation.Distribution{Counts: make(map[string]uint64)}
		left := domainevaluation.Distribution{Counts: make(map[string]uint64)}
		right := domainevaluation.Distribution{Counts: make(map[string]uint64)}
		for _, sample := range samples {
			if sample.ProbeKey != string(cell) {
				continue
			}
			if sample.NormalizedAnswer == "" {
				all.InvalidSamples++
				continue
			}
			all.Counts[sample.NormalizedAnswer]++
			if sample.SampleIndex%2 == 0 {
				left.Counts[sample.NormalizedAnswer]++
			} else {
				right.Counts[sample.NormalizedAnswer]++
			}
		}
		fingerprint.Cells[cell] = all
		if left.ValidSamples() >= 5 && right.ValidSamples() >= 5 {
			distance, err := domainevaluation.JensenShannon(left, right)
			if err != nil {
				return domainevaluation.Fingerprint{}, err
			}
			stabilitySum += distance
			stableCells++
		}
	}
	if stableCells == len(domainevaluation.RequiredCells()) {
		fingerprint.Stability = domainevaluation.Stability{Measured: true, Distance: stabilitySum / float64(stableCells)}
	}
	return fingerprint, nil
}

func responseModelConflict(expected string, samples []Sample) bool {
	expected = strings.TrimSpace(expected)
	for _, sample := range samples {
		if sample.Outcome != "succeeded" {
			continue
		}
		observed := strings.TrimSpace(sample.ResponseModel)
		if observed != "" && !strings.EqualFold(expected, observed) {
			return true
		}
	}
	return false
}
