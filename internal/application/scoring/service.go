package scoring

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
)

const scoreHistoryWindow = 24 * time.Hour

const maximumRecommendationGap = 10 * time.Minute

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() uuid.UUID
}

func NewService(repository Repository, now func() time.Time, newID func() uuid.UUID) (*Service, error) {
	if repository == nil || now == nil || newID == nil {
		return nil, errors.New("scoring dependencies are required")
	}
	return &Service{repository: repository, now: now, newID: newID}, nil
}

func (service *Service) Refresh(ctx context.Context) error {
	now := service.now().UTC()
	end := now.Truncate(time.Minute)
	start := end.Add(-scoreHistoryWindow)
	recentStart := end.Add(-10 * time.Minute)
	if err := service.repository.RefreshMinuteMetrics(ctx, start, recentStart, end, now); err != nil {
		return err
	}
	targets, err := service.repository.ListScoringTargets(ctx)
	if err != nil {
		return err
	}
	recorder, recordsRuns := service.repository.(ScoreRunRecorder)
	runs := make(map[uuid.UUID]uuid.UUID)
	completed := make(map[uuid.UUID]int)
	skipped := make(map[uuid.UUID]bool)
	if recordsRuns {
		expected := make(map[uuid.UUID]int)
		for _, target := range targets {
			if target.DesiredStatus == "enabled" && target.SyncStatus == "active" {
				expected[target.SiteID]++
			}
		}
		sites := make([]uuid.UUID, 0, len(expected))
		for siteID := range expected {
			sites = append(sites, siteID)
		}
		sort.Slice(sites, func(i, j int) bool { return sites[i].String() < sites[j].String() })
		for _, siteID := range sites {
			run := ScoreRun{
				ID: service.newID(), SiteID: siteID, PolicyVersion: domainscoring.PolicyVersionM2ShadowV1,
				WindowEnd: end, ExpectedTargets: expected[siteID], StartedAt: now,
			}
			created, err := recorder.BeginScoreRun(ctx, run)
			if err != nil {
				return err
			}
			if !created {
				skipped[siteID] = true
				continue
			}
			runs[siteID] = run.ID
		}
	}
	var failures []error
	failedBySite := make(map[uuid.UUID]int)
	for _, target := range targets {
		if target.DesiredStatus != "enabled" || target.SyncStatus != "active" {
			continue
		}
		if skipped[target.SiteID] {
			continue
		}
		var runID *uuid.UUID
		if id, ok := runs[target.SiteID]; ok {
			value := id
			runID = &value
		}
		if err := service.scoreTarget(ctx, target, end, runID); err != nil {
			failures = append(failures, fmt.Errorf("score %s/%s/%s: %w", target.SiteID, target.SupplierID, target.Model, err))
			failedBySite[target.SiteID]++
			continue
		}
		completed[target.SiteID]++
	}
	if recordsRuns {
		for siteID, runID := range runs {
			failed := failedBySite[siteID]
			summary := ""
			if failed > 0 {
				summary = fmt.Sprintf("%d 个评分目标失败", failed)
			}
			if err := recorder.FinishScoreRun(ctx, runID, completed[siteID], failed == 0, summary, service.now().UTC()); err != nil {
				failures = append(failures, fmt.Errorf("finish score run %s: %w", runID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (service *Service) ListInsights(ctx context.Context, filter InsightFilter) (InsightPage, error) {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return InsightPage{}, errors.New("scoring pagination is invalid")
	}
	filter.Model = strings.TrimSpace(filter.Model)
	return service.repository.ListInsights(ctx, filter)
}

func (service *Service) scoreTarget(ctx context.Context, target Target, end time.Time, scoreRunID *uuid.UUID) error {
	collection, err := service.repository.GetCollectionEvidence(ctx, target.SiteID)
	if err != nil {
		return err
	}
	evaluation, err := service.repository.GetEvaluationEvidence(ctx, target.SupplierID, target.Model, end)
	if err != nil {
		return err
	}
	lowestCost, err := service.repository.GetLowestPeerCost(ctx, target.SiteID, target.Model, target.Currency)
	if err != nil {
		return err
	}
	priceEvidence, err := service.repository.GetPriceEvidence(ctx, target, end.Add(-24*time.Hour), end)
	if err != nil {
		return err
	}
	price, err := scorePrice(target, lowestCost, priceEvidence)
	if err != nil {
		return err
	}
	quality, qualityAvailable := scoreQuality(evaluation, end)
	failureStreak, err := service.repository.GetFailureStreak(ctx, target, end)
	if err != nil {
		return err
	}

	windows := []struct {
		window   domainscoring.Window
		duration time.Duration
	}{
		{window: domainscoring.Window15Minutes, duration: 15 * time.Minute},
		{window: domainscoring.Window1Hour, duration: time.Hour},
		{window: domainscoring.Window24Hours, duration: 24 * time.Hour},
	}
	inputs := make([]domainscoring.WindowScoreInput, 0, len(windows))
	explanations := make([]map[string]any, 0, len(windows))
	var latestMetrics WindowMetrics
	var factsThrough time.Time
	for _, configured := range windows {
		metrics, err := service.repository.GetWindowMetrics(ctx, target, end.Add(-configured.duration), end)
		if err != nil {
			return err
		}
		if configured.window == domainscoring.Window24Hours {
			latestMetrics = metrics
		}
		if metrics.FactsThrough.After(factsThrough) {
			factsThrough = metrics.FactsThrough
		}
		input, explanation := buildWindowScore(configured.window, metrics, collection, evaluation, price, priceEvidence.Available, quality, qualityAvailable, failureStreak.Total, end)
		inputs = append(inputs, input)
		explanations = append(explanations, explanation)
	}
	combined, err := domainscoring.CombineWindowScores(inputs)
	if err != nil {
		return err
	}
	hardInput := hardGateInput(evaluation, failureStreak, end)
	policy := domainscoring.RecommendationPolicy{
		Version: domainscoring.PolicyVersionM2ShadowV1, JoinThreshold: 75, ExitThreshold: 50,
		RequiredConsecutiveWindows: 2, HardGates: domainscoring.DefaultHardGatePolicy(),
	}
	recommendations := make([]domainscoring.ShadowAdvice, 0, 5)
	for _, kind := range []domainscoring.AutoKind{
		domainscoring.AutoLowestPrice, domainscoring.AutoLowLatency, domainscoring.AutoHighSLA,
		domainscoring.AutoHighQuality, domainscoring.AutoBalanced,
	} {
		joinWindows, exitWindows, err := service.recommendationStreak(ctx, target, kind, combined, policy, end)
		if err != nil {
			return err
		}
		advice, err := domainscoring.Recommend(domainscoring.RecommendationInput{
			AutoKind: kind, CurrentMember: containsStrategy(target.CurrentStrategies, kind),
			Windows: combined, HardGates: hardInput,
			ConsecutiveJoinWindows: joinWindows, ConsecutiveExitWindows: exitWindows,
		}, policy)
		if err != nil {
			return err
		}
		recommendations = append(recommendations, advice)
	}
	eligibility := "insufficient"
	hardReasons := make([]domainscoring.GateReason, 0)
	var balancedScore *domainscoring.Score
	for index := range recommendations {
		recommendation := &recommendations[index]
		if recommendation.Action == domainscoring.AdviceExclude {
			eligibility = "excluded"
			if len(hardReasons) == 0 {
				hardReasons = append(hardReasons, recommendation.HardReasons...)
			}
		}
		if recommendation.AutoKind == domainscoring.AutoBalanced {
			balancedScore = recommendation.CompositeScore
		}
	}
	if eligibility != "excluded" && combined.DecisionReady {
		eligibility = "eligible"
	}
	var scores *domainscoring.DimensionScores
	if combined.Available {
		copyOfScores := combined.Scores
		scores = &copyOfScores
	}
	var factsThroughPointer *time.Time
	if !factsThrough.IsZero() {
		copyOfFactsThrough := factsThrough
		factsThroughPointer = &copyOfFactsThrough
	}
	return service.repository.SaveScoreSnapshot(ctx, Snapshot{
		ID: service.newID(), ScoreRunID: scoreRunID, Target: target, WindowStart: end.Add(-scoreHistoryWindow), WindowEnd: end,
		FactsThrough: factsThroughPointer, PassiveSamples: latestMetrics.SLAAttemptCount,
		ActiveSamples: uint64(max(evaluation.CapabilityChecks, 0)), Scores: scores,
		BalancedScore: balancedScore, Confidence: combined.Confidence, Eligibility: eligibility,
		HardReasons: hardReasons, Explanation: map[string]any{
			"policy_version": domainscoring.PolicyVersionM2ShadowV1,
			"policy":         scoringPolicyExplanation(policy),
			"price_evidence": priceEvidence,
			"failure_streak": failureStreak,
			"windows":        explanations, "window_issues": combined.Issues,
			"recommendations": recommendations,
		},
		AuthenticityAssessmentID: evaluation.AuthenticityID,
		CapabilityAssessmentID:   evaluation.CapabilityID,
		CreatedAt:                service.now().UTC(), Recommendations: recommendations,
	})
}

func (service *Service) recommendationStreak(
	ctx context.Context,
	target Target,
	kind domainscoring.AutoKind,
	combined domainscoring.CombinedWindowScore,
	policy domainscoring.RecommendationPolicy,
	before time.Time,
) (uint64, uint64, error) {
	if !combined.Available {
		return 0, 0, nil
	}
	weights, err := domainscoring.FixedAutoWeights(policy.Version, kind)
	if err != nil {
		return 0, 0, err
	}
	current, err := domainscoring.CompositeScore(combined.Scores, weights)
	if err != nil {
		return 0, 0, err
	}
	joinWindows, exitWindows := uint64(0), uint64(0)
	if current >= policy.JoinThreshold {
		joinWindows = 1
	}
	if current < policy.ExitThreshold {
		exitWindows = 1
	}
	previous, err := service.repository.FindPreviousRecommendation(ctx, target, kind, before)
	if err != nil || previous == nil {
		return joinWindows, exitWindows, err
	}
	if previous.CreatedAt.Before(before.Add(-maximumRecommendationGap)) ||
		(previous.Confidence != domainscoring.ConfidenceMedium && previous.Confidence != domainscoring.ConfidenceHigh) {
		return joinWindows, exitWindows, nil
	}
	if previous.Score == nil {
		return joinWindows, exitWindows, nil
	}
	if joinWindows == 1 && *previous.Score >= policy.JoinThreshold {
		joinWindows++
	}
	if exitWindows == 1 && *previous.Score < policy.ExitThreshold {
		exitWindows++
	}
	return joinWindows, exitWindows, nil
}

func containsStrategy(values []domainscoring.AutoKind, target domainscoring.AutoKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
