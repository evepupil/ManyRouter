//go:build acceptance && contract

package compatibility_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

const acceptanceSupplierCount = 3

type acceptancePage[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

type acceptanceSiteRecord struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	BaseURL     string    `json:"new_api_base_url"`
	AdminUserID int64     `json:"admin_user_id"`
	Status      string    `json:"status"`
	Version     int64     `json:"version"`
}

type acceptanceSupplierRecord struct {
	ID      uuid.UUID           `json:"id"`
	Code    string              `json:"code"`
	Name    string              `json:"name"`
	BaseURL string              `json:"upstream_base_url"`
	Status  string              `json:"status"`
	Version int64               `json:"version"`
	Models  []domain.ModelInput `json:"models"`
}

type acceptanceRelationRecord struct {
	acceptanceRelation
	DesiredStatus string `json:"desired_status"`
	SyncStatus    string `json:"sync_status"`
}

type acceptancePlanRecord struct {
	ID      uuid.UUID `json:"id"`
	SiteID  uuid.UUID `json:"site_id"`
	Version int64     `json:"version"`
	Status  string    `json:"status"`
}

type acceptanceSyncRecord struct {
	ID          uuid.UUID `json:"id"`
	SiteID      uuid.UUID `json:"site_id"`
	RoutePlanID uuid.UUID `json:"route_plan_id"`
	Status      string    `json:"status"`
}

type acceptanceAPIError struct {
	Code string `json:"code"`
}

func (run *acceptanceRun) apiRequest(method, path string, body, target any) error {
	if run.api == nil || !strings.HasPrefix(path, "/") {
		return acceptanceFault{"backend_request_invalid"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return acceptanceFault{"backend_request_invalid"}
	}
	if body == nil {
		payload = nil
	}
	idempotencyKey := "acceptance-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for attempt := 0; attempt < 120; attempt++ {
		request, err := http.NewRequestWithContext(run.ctx, method, run.api.URL+"/api/v1/ops"+path, bytes.NewReader(payload))
		if err != nil {
			return acceptanceFault{"backend_request_invalid"}
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+run.apiToken)
		if method != http.MethodGet && method != http.MethodHead {
			request.Header.Set("Idempotency-Key", idempotencyKey)
			if body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
		}
		response, err := run.client.Do(request)
		if err != nil {
			return acceptanceFault{"backend_request_failed"}
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return acceptanceFault{"backend_response_unavailable"}
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			if target == nil || response.StatusCode == http.StatusNoContent {
				return nil
			}
			if len(responseBody) == 0 || json.Unmarshal(responseBody, target) != nil {
				return acceptanceFault{"backend_response_invalid"}
			}
			return nil
		}
		var responseError acceptanceAPIError
		if response.StatusCode == http.StatusConflict && json.Unmarshal(responseBody, &responseError) == nil && responseError.Code == "configuration_busy" {
			select {
			case <-run.ctx.Done():
				return acceptanceFault{"backend_request_timeout"}
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		return acceptanceFault{"backend_request_rejected"}
	}
	return acceptanceFault{"backend_busy_timeout"}
}

func (run *acceptanceRun) seedDeployments() error {
	realSite, found, err := run.findSite(run.state.Prefix + "-real")
	if err != nil {
		return err
	}
	if found {
		var plans acceptancePage[acceptancePlanRecord]
		if err := run.apiRequest(http.MethodGet, "/plans?site_id="+realSite.ID.String()+"&limit=100", nil, &plans); err != nil {
			return err
		}
		for _, plan := range plans.Items {
			if plan.Version > run.seedPlanVersions[0] {
				run.seedPlanVersions[0] = plan.Version
			}
		}
	}
	if !found {
		input := domain.SiteInput{
			Code:          run.state.Prefix + "-real",
			Name:          run.state.Prefix + " real site",
			NewAPIBaseURL: run.sites[0].BaseURL,
			AccessToken:   run.sites[0].AdminToken,
			AdminUserID:   1,
		}
		if err := run.apiRequest(http.MethodPost, "/sites", input, &realSite); err != nil {
			return err
		}
	} else if realSite.Status != "enabled" || realSite.BaseURL != run.sites[0].BaseURL || realSite.AdminUserID != 1 {
		input := domain.SiteInput{
			Name:          run.state.Prefix + " real site",
			NewAPIBaseURL: run.sites[0].BaseURL,
			AdminUserID:   1,
			Status:        "enabled",
			Version:       realSite.Version,
			Reason:        "M1 acceptance reuses the real site",
		}
		if realSite.BaseURL != run.sites[0].BaseURL || realSite.AdminUserID != 1 {
			input.AccessToken = run.sites[0].AdminToken
		}
		if err := run.apiRequest(http.MethodPut, "/sites/"+realSite.ID.String(), input, &realSite); err != nil {
			return err
		}
	}
	run.sites[0].ID = realSite.ID

	localCode := run.state.Prefix + "-local-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	localInput := domain.SiteInput{
		Code:          localCode,
		Name:          run.state.Prefix + " temporary site",
		NewAPIBaseURL: run.values["LOCAL_MANAGEMENT_PROXY"],
		AccessToken:   run.sites[1].AdminToken,
		AdminUserID:   1,
	}
	var localSite acceptanceSiteRecord
	if err := run.apiRequest(http.MethodPost, "/sites", localInput, &localSite); err != nil {
		return err
	}
	run.sites[1].ID = localSite.ID

	run.suppliers = make([]domain.SupplierInput, acceptanceSupplierCount)
	run.supplierIDs = make([]uuid.UUID, acceptanceSupplierCount)
	existingRealRelations, err := run.relationsForSite(0)
	if err != nil {
		return err
	}
	existingBySupplier := make(map[uuid.UUID]acceptanceRelationRecord, len(existingRealRelations))
	for _, relation := range existingRealRelations {
		existingBySupplier[relation.SupplierID] = relation
	}
	for index := 0; index < acceptanceSupplierCount; index++ {
		code := fmt.Sprintf("%s-supplier-%d", run.state.Prefix, index+1)
		desired := domain.SupplierInput{
			Code:    code,
			Name:    fmt.Sprintf("%s supplier %d", run.state.Prefix, index+1),
			BaseURL: run.values["ACCEPTANCE_SUPPLIER_BASE_URL"],
			APIKey:  run.values["ACCEPTANCE_SUPPLIER_API_KEY"],
			Models: []domain.ModelInput{{
				Model:         run.values["ACCEPTANCE_PUBLIC_MODEL"],
				UpstreamModel: run.values["ACCEPTANCE_UPSTREAM_MODEL"],
				InputPrice:    run.values["ACCEPTANCE_INPUT_PRICE"],
				OutputPrice:   run.values["ACCEPTANCE_OUTPUT_PRICE"],
				Currency:      run.values["ACCEPTANCE_CURRENCY"],
				Enabled:       true,
			}},
		}
		record, exists, err := run.findSupplier(code)
		if err != nil {
			return err
		}
		if !exists {
			if err := run.apiRequest(http.MethodPost, "/suppliers", desired, &record); err != nil {
				return err
			}
		} else {
			update := desired
			update.APIKey = ""
			if record.Name == desired.Name {
				update.Name = desired.Name + " refresh"
			}
			update.Status = "enabled"
			update.Version = record.Version
			update.Reason = "M1 acceptance refreshes the shared real upstream"
			if err := run.apiRequest(http.MethodPut, "/suppliers/"+record.ID.String(), update, &record); err != nil {
				return err
			}
		}
		desired.Version = record.Version
		desired.Status = record.Status
		desired.APIKey = ""
		run.suppliers[index] = desired
		run.supplierIDs[index] = record.ID
	}

	for index, supplierID := range run.supplierIDs {
		targets := make([]domain.DeploymentTarget, 0, 2)
		if _, exists := existingBySupplier[supplierID]; !exists {
			targets = append(targets, run.deploymentTarget(0, index))
		}
		targets = append(targets, run.deploymentTarget(1, index))
		input := domain.DeploymentInput{
			SupplierID: supplierID,
			Sites:      targets,
			Reason:     "M1 acceptance deploys one supplier record to both sites",
		}
		var result struct {
			Plans []acceptancePlanRecord `json:"plans"`
		}
		if err := run.apiRequest(http.MethodPost, "/deployments", input, &result); err != nil {
			return err
		}
		if len(result.Plans) != len(targets) {
			return acceptanceFault{"deployment_plan_count_invalid"}
		}
	}
	if err := run.refreshRelations(0); err != nil {
		return err
	}
	if err := run.refreshRelations(1); err != nil {
		return err
	}
	for _, relation := range run.sites[0].Relations {
		input := domain.RelationInput{Version: relation.Version, DisplayName: relation.DisplayName, Visible: true, DesiredStatus: "enabled", Resume: false, Reason: "M1 acceptance restores its registered group entries"}
		if err := run.apiRequest(http.MethodPut, "/relations/"+relation.ID.String(), input, nil); err != nil {
			return err
		}
	}
	if err := run.refreshRelations(0); err != nil {
		return err
	}
	for _, relation := range run.sites[0].Relations {
		run.ownedTags[routing.ManagedTag(relation.ID)] = true
	}
	run.evidence.Counts["supplier_records"] = acceptanceSupplierCount
	run.evidence.Counts["real_upstreams"] = 1
	run.evidence.Counts["sites"] = len(run.sites)
	run.evidence.Checks["independent_supplier_records"] = true
	run.evidence.Checks["independent_site_processes"] = run.sites[0].BaseURL != run.sites[1].BaseURL
	if !run.evidence.Checks["independent_site_processes"] {
		return acceptanceFault{"sites_not_independent"}
	}
	return nil
}

func (run *acceptanceRun) deploymentTarget(siteSlot, supplierSlot int) domain.DeploymentTarget {
	return domain.DeploymentTarget{
		SiteID:      run.sites[siteSlot].ID,
		DisplayName: fmt.Sprintf("%s supplier %d", run.state.Prefix, supplierSlot+1),
		SaleRatio:   run.values["ACCEPTANCE_SALE_RATIO"],
		Visible:     true,
	}
}

func (run *acceptanceRun) findSite(code string) (acceptanceSiteRecord, bool, error) {
	var page acceptancePage[acceptanceSiteRecord]
	if err := run.apiRequest(http.MethodGet, "/sites?q="+url.QueryEscape(code)+"&limit=100", nil, &page); err != nil {
		return acceptanceSiteRecord{}, false, err
	}
	for _, item := range page.Items {
		if item.Code == code {
			return item, true, nil
		}
	}
	return acceptanceSiteRecord{}, false, nil
}

func (run *acceptanceRun) findSupplier(code string) (acceptanceSupplierRecord, bool, error) {
	var page acceptancePage[acceptanceSupplierRecord]
	if err := run.apiRequest(http.MethodGet, "/suppliers?q="+url.QueryEscape(code)+"&limit=100", nil, &page); err != nil {
		return acceptanceSupplierRecord{}, false, err
	}
	for _, item := range page.Items {
		if item.Code == code {
			return item, true, nil
		}
	}
	return acceptanceSupplierRecord{}, false, nil
}

func (run *acceptanceRun) relationsForSite(slot int) ([]acceptanceRelationRecord, error) {
	if slot < 0 || slot >= len(run.sites) || run.sites[slot].ID == uuid.Nil {
		return nil, acceptanceFault{"site_slot_invalid"}
	}
	var page acceptancePage[acceptanceRelationRecord]
	path := "/relations?site_id=" + url.QueryEscape(run.sites[slot].ID.String()) + "&limit=100"
	if err := run.apiRequest(http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (run *acceptanceRun) refreshRelations(slot int) error {
	relations, err := run.relationsForSite(slot)
	if err != nil {
		return err
	}
	bySupplier := make(map[uuid.UUID]acceptanceRelation, len(relations))
	for _, relation := range relations {
		if _, duplicate := bySupplier[relation.SupplierID]; duplicate {
			return acceptanceFault{"relation_duplicate"}
		}
		bySupplier[relation.SupplierID] = relation.acceptanceRelation
	}
	ordered := make([]acceptanceRelation, 0, len(run.supplierIDs))
	for _, supplierID := range run.supplierIDs {
		relation, found := bySupplier[supplierID]
		if !found || relation.ID == uuid.Nil || relation.CurrentPlanID == uuid.Nil {
			return acceptanceFault{"relation_missing"}
		}
		ordered = append(ordered, relation)
		delete(bySupplier, supplierID)
	}
	if len(ordered) != acceptanceSupplierCount || len(bySupplier) != 0 {
		return acceptanceFault{"relation_count_mismatch"}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return indexUUID(run.supplierIDs, ordered[i].SupplierID) < indexUUID(run.supplierIDs, ordered[j].SupplierID)
	})
	run.sites[slot].Relations = ordered
	return nil
}

func (run *acceptanceRun) waitSite(slot int, wantFailure bool) error {
	if slot < 0 || slot >= len(run.sites) || run.sites[slot].ID == uuid.Nil {
		return acceptanceFault{"site_slot_invalid"}
	}
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		complete, err := run.siteReachedExpectedState(slot, wantFailure)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
		select {
		case <-run.ctx.Done():
			return acceptanceFault{"site_sync_timeout"}
		case <-timer.C:
			return acceptanceFault{"site_sync_timeout"}
		case <-ticker.C:
		}
	}
}

func (run *acceptanceRun) siteReachedExpectedState(slot int, wantFailure bool) (bool, error) {
	siteID := run.sites[slot].ID
	var plans acceptancePage[acceptancePlanRecord]
	if err := run.apiRequest(http.MethodGet, "/plans?site_id="+url.QueryEscape(siteID.String())+"&limit=100", nil, &plans); err != nil {
		return false, err
	}
	if len(plans.Items) == 0 {
		return false, nil
	}
	sort.Slice(plans.Items, func(i, j int) bool { return plans.Items[i].Version > plans.Items[j].Version })
	latest := plans.Items[0]
	if latest.ID == uuid.Nil || latest.SiteID != siteID || latest.Version < 1 {
		return false, acceptanceFault{"site_plan_invalid"}
	}
	var operations acceptancePage[acceptanceSyncRecord]
	if err := run.apiRequest(http.MethodGet, "/sync-operations?site_id="+url.QueryEscape(siteID.String())+"&limit=100", nil, &operations); err != nil {
		return false, err
	}
	byPlan := make(map[uuid.UUID]acceptanceSyncRecord, len(operations.Items))
	for _, operation := range operations.Items {
		if operation.ID == uuid.Nil || operation.SiteID != siteID || operation.RoutePlanID == uuid.Nil {
			return false, acceptanceFault{"sync_operation_invalid"}
		}
		if _, duplicate := byPlan[operation.RoutePlanID]; duplicate {
			return false, acceptanceFault{"sync_operation_duplicate"}
		}
		byPlan[operation.RoutePlanID] = operation
	}
	latestOperation, found := byPlan[latest.ID]
	if !found {
		return false, nil
	}
	if wantFailure {
		if latestOperation.Status != "retryable_failed" || latest.Status != "failed" {
			return false, nil
		}
		relations, err := run.relationsForSite(slot)
		if err != nil {
			return false, err
		}
		if !relationsMatchPlan(relations, run.supplierIDs, latest.ID, "failed") {
			return false, acceptanceFault{"failed_site_state_invalid"}
		}
		return true, nil
	}
	if latestOperation.Status == "manual_required" || latestOperation.Status == "uncertain" {
		return false, acceptanceFault{"site_sync_terminal_failure"}
	}
	if latest.Status != "confirmed" || latestOperation.Status != "succeeded" {
		return false, nil
	}
	relations, err := run.relationsForSite(slot)
	if err != nil {
		return false, err
	}
	if !relationsMatchPlan(relations, run.supplierIDs, latest.ID, "active") {
		return false, acceptanceFault{"active_relations_invalid"}
	}
	initialCheck := fmt.Sprintf("site_%d_initial_supersession", slot)
	if !run.evidence.Checks[initialCheck] {
		superseded := 0
		for _, plan := range plans.Items[1:] {
			if plan.Version <= run.seedPlanVersions[slot] {
				continue
			}
			operation, exists := byPlan[plan.ID]
			if !exists || plan.Status != "superseded" || operation.Status != "superseded" {
				return false, nil
			}
			superseded++
		}
		if superseded < acceptanceSupplierCount-1 {
			return false, acceptanceFault{"superseded_operation_count_insufficient"}
		}
		run.evidence.Checks[initialCheck] = true
		if slot == 0 {
			run.evidence.Counts["real_superseded_operations"] = superseded
		} else {
			run.evidence.Counts["local_superseded_operations"] = superseded
		}
	}
	if err := run.refreshRelations(slot); err != nil {
		return false, err
	}
	return true, nil
}

func relationsMatchPlan(relations []acceptanceRelationRecord, supplierIDs []uuid.UUID, planID uuid.UUID, status string) bool {
	if len(relations) != len(supplierIDs) || len(relations) != acceptanceSupplierCount {
		return false
	}
	wanted := make(map[uuid.UUID]bool, len(supplierIDs))
	for _, supplierID := range supplierIDs {
		wanted[supplierID] = true
	}
	for _, relation := range relations {
		if relation.ID == uuid.Nil || relation.CurrentPlanID != planID || relation.SyncStatus != status || !wanted[relation.SupplierID] {
			return false
		}
		delete(wanted, relation.SupplierID)
	}
	return len(wanted) == 0
}

func indexUUID(ids []uuid.UUID, id uuid.UUID) int {
	for index, candidate := range ids {
		if candidate == id {
			return index
		}
	}
	return len(ids)
}
