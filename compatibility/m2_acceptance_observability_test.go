//go:build acceptance && contract

package compatibility_test

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	collectionapp "github.com/evepupil/ManyRouter/internal/application/collection"
	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (run *acceptanceRun) exerciseM2Traffic() error {
	if err := run.exerciseUserRequests(); err != nil {
		return err
	}
	additionalCalls := 0
	for slot := range run.sites {
		for relationIndex := 1; relationIndex < len(run.sites[slot].Relations); relationIndex++ {
			key, err := run.createUserKey(slot, "m2-supplier", run.sites[slot].Relations[relationIndex].GroupKey)
			if err != nil {
				return err
			}
			if err := run.callModel(slot, key, http.StatusOK); err != nil {
				return err
			}
			additionalCalls++
		}
	}
	run.evidence.Counts["temporary_keys_created"] = len(run.keys)
	run.evidence.Counts["real_model_calls"] = len(run.sites)*2 + additionalCalls
	run.evidence.Checks["all_supplier_records_received_real_traffic"] = additionalCalls == len(run.sites)*(acceptanceSupplierCount-1)
	return nil
}

func (run *acceptanceRun) verifyM2Collection() error {
	for slot := range run.sites {
		var result collectionapp.Result
		if err := run.apiRequest(http.MethodPost, "/collection-runs", map[string]any{"site_id": run.sites[slot].ID}, &result); err != nil {
			return err
		}
		if result.SiteID != run.sites[slot].ID || result.ScannedThrough == nil || result.DataGap {
			return acceptanceFault{"collection_result_invalid"}
		}
	}
	for slot := range run.sites {
		logs, err := run.m2ModelLogs(slot)
		if err != nil {
			return err
		}
		sourceRequests := make(map[string]struct{})
		for _, entry := range logs {
			if entry.Model == run.values["ACCEPTANCE_PUBLIC_MODEL"] && entry.RequestID != "" {
				sourceRequests[entry.RequestID] = struct{}{}
			}
		}
		rows, err := run.store.Pool().Query(run.ctx, `
			SELECT request_id
			FROM measurement_requests
			WHERE site_id=$1 AND source='real_traffic' AND model=$2 AND is_current
		`, run.sites[slot].ID, run.values["ACCEPTANCE_PUBLIC_MODEL"])
		if err != nil {
			return err
		}
		measuredRequests := make(map[string]struct{})
		for rows.Next() {
			var requestID string
			if err := rows.Scan(&requestID); err != nil {
				rows.Close()
				return err
			}
			measuredRequests[requestID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(sourceRequests) < acceptanceSupplierCount || !maps.Equal(sourceRequests, measuredRequests) {
			return acceptanceFault{"collection_source_reconciliation_failed"}
		}
		var attempts int
		if err := run.store.Pool().QueryRow(run.ctx, `
			SELECT count(*)
			FROM measurement_attempts attempt
			JOIN measurement_requests request ON request.id=attempt.request_measurement_id
			WHERE request.site_id=$1 AND request.source='real_traffic' AND request.is_current
		`, run.sites[slot].ID).Scan(&attempts); err != nil {
			return err
		}
		if attempts < len(measuredRequests) {
			return acceptanceFault{"collection_attempt_chain_incomplete"}
		}
		var statuses struct {
			Items []collectionapp.Status `json:"items"`
		}
		if err := run.apiRequest(http.MethodGet, "/collection-status?site_id="+run.sites[slot].ID.String(), nil, &statuses); err != nil {
			return err
		}
		if len(statuses.Items) != 1 || statuses.Items[0].ScannedThrough == nil || statuses.Items[0].LastSuccessAt == nil || statuses.Items[0].DataGap {
			return acceptanceFault{"collection_status_invalid"}
		}
		run.evidence.Counts["site_"+string(rune('1'+slot))+"_source_requests"] = len(sourceRequests)
		run.evidence.Counts["site_"+string(rune('1'+slot))+"_measurement_attempts"] = attempts
	}
	run.evidence.Checks["source_logs_reconciled"] = true
	run.evidence.Checks["collection_watermarks_advanced"] = true
	return nil
}

func (run *acceptanceRun) m2ModelLogs(slot int) ([]newapi.AdminLogEntry, error) {
	result := make([]newapi.AdminLogEntry, 0)
	end := time.Now().UTC().Add(time.Second).Unix()
	for _, logType := range []int{2, 5} {
		for page := 1; page <= 100; page++ {
			response, err := run.sites[slot].Client.ReadAdminLogs(run.ctx, logType, 0, end, page, 100)
			if err != nil {
				return nil, err
			}
			result = append(result, response.Items...)
			if len(response.Items) == 0 || int64(page*100) >= response.Total {
				break
			}
			if page == 100 {
				return nil, acceptanceFault{"source_log_page_limit"}
			}
		}
	}
	return result, nil
}

func (run *acceptanceRun) verifyM2BaselineEvaluations() error {
	deadline := time.NewTimer(12 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var page evaluationapp.RunPage
		if err := run.apiRequest(http.MethodGet, "/evaluation-runs?limit=100", nil, &page); err != nil {
			return err
		}
		completed := make(map[uuid.UUID]map[evaluationapp.Purpose]bool, len(run.supplierIDs))
		for _, item := range page.Items {
			if item.Purpose != evaluationapp.PurposeHealth && item.Purpose != evaluationapp.PurposeQuality {
				continue
			}
			if item.Status == evaluationapp.RunFailed || item.Status == evaluationapp.RunCancelled || item.Status == evaluationapp.RunUncertain && item.NextRetryAt == nil {
				return acceptanceFault{"baseline_evaluation_failed"}
			}
			if item.Status == evaluationapp.RunSucceeded && item.CompletedSamples == item.PlannedSamples {
				if completed[item.SupplierID] == nil {
					completed[item.SupplierID] = make(map[evaluationapp.Purpose]bool)
				}
				completed[item.SupplierID][item.Purpose] = true
			}
		}
		ready := true
		for _, supplierID := range run.supplierIDs {
			ready = ready && completed[supplierID][evaluationapp.PurposeHealth] && completed[supplierID][evaluationapp.PurposeQuality]
		}
		if ready {
			var healthPositive, qualityComplete int
			if err := run.store.Pool().QueryRow(run.ctx, `
				SELECT
					count(*) FILTER (WHERE suite_version=$1 AND score > 0 AND confidence > 0),
					count(*) FILTER (WHERE suite_version=$2 AND completed_checks=total_checks)
				FROM capability_assessments
			`, evaluationapp.HealthSuiteVersion, evaluationapp.CapabilitySuiteVersion).Scan(&healthPositive, &qualityComplete); err != nil {
				return err
			}
			if healthPositive != len(run.supplierIDs) || qualityComplete != len(run.supplierIDs) {
				return acceptanceFault{"baseline_evaluation_evidence_invalid"}
			}
			run.evidence.Counts["health_evaluations"] = healthPositive
			run.evidence.Counts["quality_evaluations"] = qualityComplete
			run.evidence.Checks["health_and_quality_results_queryable"] = true
			return nil
		}
		select {
		case <-run.ctx.Done():
			return acceptanceFault{"baseline_evaluation_timeout"}
		case <-deadline.C:
			return acceptanceFault{"baseline_evaluation_timeout"}
		case <-ticker.C:
		}
	}
}

func (run *acceptanceRun) verifyM2Authenticity() error {
	baseline, err := run.requestAndWaitM2Evaluation(run.supplierIDs[0], evaluationapp.PurposeAuthenticity)
	if err != nil {
		return err
	}
	var baselineVerdict string
	var baselineStable bool
	var baselineValidSamples int
	if err := run.store.Pool().QueryRow(run.ctx, `
		SELECT assessment.verdict, fingerprint.stable, fingerprint.valid_samples
		FROM authenticity_assessments assessment
		JOIN evaluation_fingerprints fingerprint ON fingerprint.run_id=assessment.run_id
		WHERE assessment.run_id=$1
	`, baseline.ID).Scan(&baselineVerdict, &baselineStable, &baselineValidSamples); err != nil {
		return err
	}
	if baselineVerdict != string(domainevaluation.VerdictInsufficient) || !baselineStable || baselineValidSamples < 80 {
		return acceptanceFault{"trusted_reference_candidate_unstable"}
	}
	var reference struct {
		ID uuid.UUID `json:"id"`
	}
	if err := run.apiRequest(http.MethodPost, "/evaluation-runs/"+baseline.ID.String()+"/reference", map[string]any{
		"trust": "operator_trusted", "reason": "M2 acceptance shared-upstream baseline", "valid_days": 7,
	}, &reference); err != nil {
		return err
	}
	if reference.ID == uuid.Nil {
		return acceptanceFault{"trusted_reference_missing"}
	}
	target, err := run.requestAndWaitM2Evaluation(run.supplierIDs[1], evaluationapp.PurposeAuthenticity)
	if err != nil {
		return err
	}
	var verdict, referenceID string
	var meanDistance, selfDistance pgtype.Float8
	var validSamples int
	if err := run.store.Pool().QueryRow(run.ctx, `
		SELECT assessment.verdict, COALESCE(assessment.reference_id::text,''),
		       assessment.mean_distance::float8, assessment.self_distance::float8,
		       fingerprint.valid_samples
		FROM authenticity_assessments assessment
		JOIN evaluation_fingerprints fingerprint ON fingerprint.run_id=assessment.run_id
		WHERE assessment.run_id=$1
	`, target.ID).Scan(&verdict, &referenceID, &meanDistance, &selfDistance, &validSamples); err != nil {
		return err
	}
	if referenceID != reference.ID.String() || !meanDistance.Valid || !selfDistance.Valid || validSamples < 80 {
		return acceptanceFault{"authenticity_comparison_evidence_invalid"}
	}
	if verdict != string(domainevaluation.VerdictConsistent) && verdict != string(domainevaluation.VerdictSuspicious) {
		return acceptanceFault{"authenticity_comparison_failed"}
	}
	run.evidence.Counts["reference_fingerprint_samples"] = baseline.CompletedSamples
	run.evidence.Counts["comparison_fingerprint_samples"] = target.CompletedSamples
	run.evidence.Checks["trusted_reference_promoted"] = true
	run.evidence.Checks["shared_upstream_authenticity_compared"] = true
	run.evidence.Checks["authenticity_consistent"] = verdict == string(domainevaluation.VerdictConsistent)
	run.evidence.Checks["authenticity_suspicious"] = verdict == string(domainevaluation.VerdictSuspicious)
	return nil
}

func (run *acceptanceRun) requestAndWaitM2Evaluation(supplierID uuid.UUID, purpose evaluationapp.Purpose) (evaluationapp.Run, error) {
	var requested evaluationapp.Run
	var existing evaluationapp.RunPage
	path := "/evaluation-runs?supplier_id=" + supplierID.String() + "&model=" + url.QueryEscape(run.values["ACCEPTANCE_PUBLIC_MODEL"]) + "&purpose=" + url.QueryEscape(string(purpose)) + "&limit=20"
	if err := run.apiRequest(http.MethodGet, path, nil, &existing); err != nil {
		return evaluationapp.Run{}, err
	}
	for _, candidate := range existing.Items {
		if candidate.Status == evaluationapp.RunPending || candidate.Status == evaluationapp.RunRunning || candidate.Status == evaluationapp.RunUncertain || candidate.Status == evaluationapp.RunSucceeded {
			requested = candidate
			break
		}
	}
	if requested.ID == uuid.Nil {
		if err := run.apiRequest(http.MethodPost, "/evaluation-runs", map[string]any{
			"supplier_id": supplierID, "model": run.values["ACCEPTANCE_PUBLIC_MODEL"],
			"purpose": purpose, "target_kind": "supplier_direct", "reason": "M2 real acceptance",
		}, &requested); err != nil {
			return evaluationapp.Run{}, err
		}
	}
	deadline := time.NewTimer(25 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var current evaluationapp.Run
		if err := run.apiRequest(http.MethodGet, "/evaluation-runs/"+requested.ID.String(), nil, &current); err != nil {
			return evaluationapp.Run{}, err
		}
		switch current.Status {
		case evaluationapp.RunSucceeded:
			if current.CompletedSamples != current.PlannedSamples {
				return evaluationapp.Run{}, acceptanceFault{"evaluation_sample_count_invalid"}
			}
			return current, nil
		case evaluationapp.RunFailed, evaluationapp.RunCancelled:
			return evaluationapp.Run{}, acceptanceFault{"evaluation_terminal_failure"}
		case evaluationapp.RunUncertain:
			if current.NextRetryAt == nil {
				return evaluationapp.Run{}, acceptanceFault{"evaluation_result_uncertain"}
			}
		}
		select {
		case <-run.ctx.Done():
			return evaluationapp.Run{}, acceptanceFault{"evaluation_timeout"}
		case <-deadline.C:
			return evaluationapp.Run{}, acceptanceFault{"evaluation_timeout"}
		case <-ticker.C:
		}
	}
}

func (run *acceptanceRun) verifyM2Scoring() error {
	before := make([]any, len(run.sites))
	planVersions := make(map[uuid.UUID]int64, len(run.sites))
	for slot := range run.sites {
		state, err := run.sites[slot].Client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		before[slot] = state
		var planVersion int64
		if err := run.store.Pool().QueryRow(run.ctx, "SELECT max(version) FROM route_plan_versions WHERE site_id=$1", run.sites[slot].ID).Scan(&planVersion); err != nil {
			return err
		}
		planVersions[run.sites[slot].ID] = planVersion
	}
	if err := waitForM2NextMinute(run.ctx); err != nil {
		return err
	}
	if err := run.apiRequest(http.MethodPost, "/score-runs", map[string]any{}, nil); err != nil {
		return err
	}
	if err := waitForM2NextMinute(run.ctx); err != nil {
		return err
	}
	if err := run.apiRequest(http.MethodPost, "/score-runs", map[string]any{}, nil); err != nil {
		return err
	}
	for slot := range run.sites {
		var page scoringapp.InsightPage
		if err := run.apiRequest(http.MethodGet, "/score-insights?site_id="+run.sites[slot].ID.String()+"&limit=100", nil, &page); err != nil {
			return err
		}
		if len(page.Items) != acceptanceSupplierCount {
			return acceptanceFault{"score_target_count_invalid"}
		}
		for _, insight := range page.Items {
			if insight.PolicyVersion == "" || insight.WindowStart.IsZero() || insight.WindowEnd.IsZero() || len(insight.Explanation) == 0 || len(insight.Recommendations) != 5 {
				return acceptanceFault{"score_explanation_incomplete"}
			}
		}
		var snapshots int
		if err := run.store.Pool().QueryRow(run.ctx, `
			SELECT count(*) FROM score_snapshots WHERE site_id=$1
		`, run.sites[slot].ID).Scan(&snapshots); err != nil {
			return err
		}
		if snapshots < acceptanceSupplierCount*2 {
			return acceptanceFault{"score_history_incomplete"}
		}
		run.evidence.Counts["site_"+string(rune('1'+slot))+"_score_snapshots"] = snapshots
		state, err := run.sites[slot].Client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before[slot], state) {
			return acceptanceFault{"shadow_score_changed_gateway"}
		}
		var currentVersion int64
		if err := run.store.Pool().QueryRow(run.ctx, "SELECT max(version) FROM route_plan_versions WHERE site_id=$1", run.sites[slot].ID).Scan(&currentVersion); err != nil {
			return err
		}
		if currentVersion != planVersions[run.sites[slot].ID] {
			return acceptanceFault{"shadow_score_created_route_plan"}
		}
	}
	run.evidence.Checks["two_score_rounds_persisted"] = true
	run.evidence.Checks["score_explanations_queryable"] = true
	run.evidence.Checks["shadow_scoring_did_not_change_routes"] = true
	return nil
}

func waitForM2NextMinute(ctx context.Context) error {
	next := time.Now().UTC().Truncate(time.Minute).Add(time.Minute).Add(time.Second)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
