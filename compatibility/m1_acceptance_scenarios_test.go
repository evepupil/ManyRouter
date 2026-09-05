//go:build acceptance && contract

package compatibility_test

import (
	"crypto/sha256"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

type acceptanceStrategy struct {
	ID          uuid.UUID   `json:"id"`
	Version     int64       `json:"version"`
	Kind        string      `json:"kind"`
	GroupKey    string      `json:"group_key"`
	DisplayName string      `json:"display_name"`
	Enabled     bool        `json:"enabled"`
	Visible     bool        `json:"visible"`
	Members     []uuid.UUID `json:"member_relation_ids"`
}

func (run *acceptanceRun) strategy(slot int) (acceptanceStrategy, error) {
	var page struct {
		Items []acceptanceStrategy `json:"items"`
	}
	if err := run.apiRequest(http.MethodGet, "/strategies?site_id="+run.sites[slot].ID.String()+"&limit=100", nil, &page); err != nil {
		return acceptanceStrategy{}, err
	}
	for _, strategy := range page.Items {
		if strategy.Kind == "balanced" {
			return strategy, nil
		}
	}
	return acceptanceStrategy{}, nil
}

func (run *acceptanceRun) saveStrategy(slot int, members []uuid.UUID, enabled, visible bool) error {
	current, err := run.strategy(slot)
	if err != nil {
		return err
	}
	input := domain.StrategyInput{Version: current.Version, Enabled: enabled, Visible: visible, DisplayName: run.state.Prefix + " balanced", MemberRelationIDs: members, Reason: "M1 acceptance controlled strategy update"}
	var result acceptanceStrategy
	if err := run.apiRequest(http.MethodPut, "/sites/"+run.sites[slot].ID.String()+"/strategies/balanced", input, &result); err != nil {
		return err
	}
	run.sites[slot].AutoGroup = result.GroupKey
	return run.waitSite(slot, false)
}

func (run *acceptanceRun) publishPrice(slot int, group, ratio string) error {
	var price struct {
		ID      uuid.UUID `json:"id"`
		Version int64     `json:"version"`
	}
	input := domain.PriceInput{SiteID: run.sites[slot].ID, GroupKey: group, SaleRatio: ratio, Reason: "M1 acceptance controlled price"}
	if err := run.apiRequest(http.MethodPost, "/prices", input, &price); err != nil {
		return err
	}
	if price.ID == uuid.Nil || price.Version < 1 {
		return acceptanceFault{"price_response_invalid"}
	}
	if err := run.apiRequest(http.MethodPost, "/prices/"+price.ID.String()+"/publish", domain.PublishInput{Version: price.Version}, nil); err != nil {
		return err
	}
	return run.waitSite(slot, false)
}

func (run *acceptanceRun) configureStrategies() error {
	for slot := range run.sites {
		if err := run.refreshRelations(slot); err != nil {
			return err
		}
		if len(run.sites[slot].Relations) != 3 {
			return acceptanceFault{"relation_count_invalid"}
		}
		members := []uuid.UUID{run.sites[slot].Relations[0].ID, run.sites[slot].Relations[1].ID}
		if slot == 1 {
			members = []uuid.UUID{run.sites[slot].Relations[1].ID, run.sites[slot].Relations[2].ID}
		}
		current, err := run.strategy(slot)
		if err != nil {
			return err
		}
		if current.ID == uuid.Nil {
			if err := run.saveStrategy(slot, members, false, true); err != nil {
				return err
			}
		} else {
			run.sites[slot].AutoGroup = current.GroupKey
		}
		ratio := "1.1"
		if slot == 1 {
			ratio = "1.3"
		}
		if err := run.publishPrice(slot, run.sites[slot].AutoGroup, ratio); err != nil {
			return err
		}
		if err := run.saveStrategy(slot, members, true, true); err != nil {
			return err
		}
	}
	run.evidence.Checks["different_manual_members_configured"] = true
	run.evidence.Checks["different_auto_prices_configured"] = true
	return run.verifyExpectedMemberships([][]int{{0, 1}, {1, 2}})
}

func (run *acceptanceRun) setDedicatedEntry(slot int, open bool) error {
	if err := run.refreshRelations(slot); err != nil {
		return err
	}
	relation := run.sites[slot].Relations[0]
	input := domain.RelationInput{Version: relation.Version, DisplayName: relation.DisplayName, Visible: open, DesiredStatus: "enabled", Resume: false, Reason: "M1 acceptance entry access check"}
	if err := run.apiRequest(http.MethodPut, "/relations/"+relation.ID.String(), input, nil); err != nil {
		return err
	}
	return run.waitSite(slot, false)
}

func (run *acceptanceRun) exerciseIsolation() error {
	if err := run.setDedicatedRestoreIntent(true); err != nil {
		return err
	}
	if err := run.setDedicatedEntry(0, false); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].DedicatedKey, http.StatusForbidden); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].AutoKey, http.StatusOK); err != nil {
		return err
	}
	if err := run.callModel(1, run.sites[1].DedicatedKey, http.StatusOK); err != nil {
		return err
	}
	if err := run.setDedicatedEntry(0, true); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].DedicatedKey, http.StatusOK); err != nil {
		return err
	}
	run.evidence.Checks["dedicated_entry_closed_and_reopened"] = true
	if err := run.setDedicatedRestoreIntent(false); err != nil {
		return err
	}
	strategy, err := run.strategy(0)
	if err != nil {
		return err
	}
	if err := run.setAutoRestoreIntent(true); err != nil {
		return err
	}
	if err := run.saveStrategy(0, strategy.Members, true, false); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].AutoKey, http.StatusForbidden); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].DedicatedKey, http.StatusOK); err != nil {
		return err
	}
	if err := run.saveStrategy(0, strategy.Members, true, true); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].AutoKey, http.StatusOK); err != nil {
		return err
	}
	run.evidence.Checks["auto_entry_closed_and_reopened"] = true
	if err := run.setAutoRestoreIntent(false); err != nil {
		return err
	}
	beforeLocal, err := run.sites[1].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	if err := run.publishPrice(0, run.sites[0].AutoGroup, "1.2"); err != nil {
		return err
	}
	if err := run.refreshRelations(0); err != nil {
		return err
	}
	historicalPlan := run.sites[0].Relations[0].CurrentPlanID
	if err := run.saveStrategy(0, []uuid.UUID{run.sites[0].Relations[0].ID, run.sites[0].Relations[2].ID}, true, true); err != nil {
		return err
	}
	afterLocal, err := run.sites[1].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeLocal, afterLocal) {
		return acceptanceFault{"local_site_changed_by_real_site_edit"}
	}
	actual, err := run.sites[0].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	if actual.GroupRatios[run.sites[0].AutoGroup] != "1.2" || afterLocal.GroupRatios[run.sites[1].AutoGroup] != "1.3" {
		return acceptanceFault{"site_price_isolation_failed"}
	}
	if err := run.verifyExpectedMemberships([][]int{{0, 2}, {1, 2}}); err != nil {
		return err
	}
	run.evidence.Checks["price_and_membership_changes_isolated"] = true
	if err := run.setDedicatedRestoreIntent(true); err != nil {
		return err
	}
	if err := run.setDedicatedEntry(0, false); err != nil {
		return err
	}
	if err := run.apiRequest(http.MethodPost, "/plans/"+historicalPlan.String()+"/restore", domain.RestoreInput{Reason: "M1 acceptance restores recorded configuration"}, nil); err != nil {
		return err
	}
	if err := run.waitSite(0, false); err != nil {
		return err
	}
	if err := run.verifyExpectedMemberships([][]int{{0, 1}, {1, 2}}); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].DedicatedKey, http.StatusOK); err != nil {
		return err
	}
	if err := run.setDedicatedRestoreIntent(false); err != nil {
		return err
	}
	run.evidence.Checks["historical_configuration_restored"] = true
	return nil
}

func (run *acceptanceRun) exerciseFailure() error {
	run.failLocal.Store(true)
	defer run.failLocal.Store(false)
	var page struct {
		Items []struct {
			ID      uuid.UUID `json:"id"`
			Version int64     `json:"version"`
			Name    string    `json:"name"`
		} `json:"items"`
	}
	if err := run.apiRequest(http.MethodGet, "/suppliers?q="+url.QueryEscape(run.state.Prefix+"-supplier-1")+"&limit=100", nil, &page); err != nil {
		return err
	}
	var version int64
	var name string
	for _, item := range page.Items {
		if item.ID == run.supplierIDs[0] {
			version, name = item.Version, item.Name
		}
	}
	if version < 1 {
		return acceptanceFault{"supplier_record_missing"}
	}
	input := run.suppliers[0]
	input.APIKey = ""
	input.Version = version
	input.Status = "enabled"
	input.Reason = "M1 acceptance isolated local management failure"
	input.Name = run.state.Prefix + " supplier 1 verified"
	if input.Name == name {
		input.Name = run.state.Prefix + " supplier 1"
	}
	if err := run.apiRequest(http.MethodPut, "/suppliers/"+run.supplierIDs[0].String(), input, nil); err != nil {
		return err
	}
	if err := run.waitSite(0, false); err != nil {
		return err
	}
	if err := run.waitSite(1, true); err != nil {
		return err
	}
	if err := run.callModel(0, run.sites[0].AutoKey, http.StatusOK); err != nil {
		return err
	}
	run.evidence.Checks["single_site_failure_isolated"] = true
	run.failLocal.Store(false)
	if err := run.apiRequest(http.MethodPost, "/sites/"+run.sites[1].ID.String()+"/sync", nil, nil); err != nil {
		return err
	}
	if err := run.waitSite(1, false); err != nil {
		return err
	}
	run.evidence.Checks["failed_site_recovered"] = true
	return nil
}

func (run *acceptanceRun) exerciseRepeat() error {
	before := make([]int, len(run.sites))
	for slot := range run.sites {
		actual, err := run.sites[slot].Client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		before[slot] = len(actual.Channels)
	}
	for slot := range run.sites {
		if err := run.apiRequest(http.MethodPost, "/sites/"+run.sites[slot].ID.String()+"/sync", nil, nil); err != nil {
			return err
		}
		if err := run.waitSite(slot, false); err != nil {
			return err
		}
		actual, err := run.sites[slot].Client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		if len(actual.Channels) != before[slot] {
			return acceptanceFault{"repeat_created_channels"}
		}
	}
	run.evidence.Checks["repeat_sync_reused_channels"] = true
	return nil
}

func (run *acceptanceRun) verifyExpectedMemberships(expected [][]int) error {
	for slot, members := range expected {
		actual, err := run.sites[slot].Client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		for index, relation := range run.sites[slot].Relations {
			tag := routing.ManagedTag(relation.ID)
			count := 0
			for _, channel := range actual.Channels {
				if channel.ManagedTag == tag {
					count++
					if slices.Contains(channel.Groups, run.sites[slot].AutoGroup) != slices.Contains(members, index) {
						return acceptanceFault{"auto_membership_mismatch"}
					}
				}
			}
			if count != 1 {
				return acceptanceFault{"managed_channel_not_unique"}
			}
		}
	}
	return nil
}

func (run *acceptanceRun) verifyPreserved() error {
	run.evidence.Checks["existing_channels_preserved"] = false
	run.evidence.Checks["existing_group_entries_preserved"] = false
	run.evidence.Checks["other_gateway_options_preserved"] = false
	actual, err := run.sites[0].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	expectedTags := make(map[string]bool, len(run.sites[0].Relations))
	for _, relation := range run.sites[0].Relations {
		expectedTags[routing.ManagedTag(relation.ID)] = true
	}
	owned := 0
	seenTags := make(map[string]bool, len(expectedTags))
	for _, channel := range actual.Channels {
		if expectedTags[channel.ManagedTag] {
			if seenTags[channel.ManagedTag] {
				return acceptanceFault{"managed_channel_not_unique"}
			}
			seenTags[channel.ManagedTag] = true
			owned++
			continue
		}
		if strings.HasPrefix(channel.Name, run.state.Prefix) {
			return acceptanceFault{"unexpected_acceptance_channel"}
		}
	}
	baselineUnmanaged := 0
	for _, before := range run.baseline.Channels {
		if run.ownedTags[before.ManagedTag] {
			continue
		}
		baselineUnmanaged++
		found := false
		for _, after := range actual.Channels {
			if after.ID == before.ID {
				found = true
				if !reflect.DeepEqual(before, after) {
					return acceptanceFault{"existing_channel_changed"}
				}
			}
		}
		if !found {
			return acceptanceFault{"existing_channel_missing"}
		}
	}
	ownedGroups := map[string]bool{run.sites[0].AutoGroup: true}
	for _, relation := range run.sites[0].Relations {
		ownedGroups[relation.GroupKey] = true
	}
	for key, value := range run.baseline.GroupRatios {
		if !ownedGroups[key] && actual.GroupRatios[key] != value {
			return acceptanceFault{"existing_group_price_changed"}
		}
	}
	for key, value := range run.baseline.UserUsableGroups {
		if !ownedGroups[key] && actual.UserUsableGroups[key] != value {
			return acceptanceFault{"existing_group_access_changed"}
		}
	}
	if owned != 3 {
		return acceptanceFault{"managed_channel_count_invalid"}
	}
	if len(actual.Channels) != baselineUnmanaged+acceptanceSupplierCount {
		return acceptanceFault{"real_channel_total_invalid"}
	}
	local, err := run.sites[1].Client.ReadActualState(run.ctx)
	if err != nil {
		return err
	}
	if len(local.Channels) != acceptanceSupplierCount {
		return acceptanceFault{"local_channel_total_invalid"}
	}
	run.evidence.Counts["real_channels_after"] = len(actual.Channels)
	run.evidence.Counts["real_managed_channels"] = owned
	run.evidence.Checks["existing_channels_preserved"] = true
	run.evidence.Checks["existing_group_entries_preserved"] = true
	options, err := run.optionDigests()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(run.baselineOptions, options) {
		return acceptanceFault{"existing_other_options_changed"}
	}
	run.evidence.Checks["other_gateway_options_preserved"] = true
	run.evidence.Checks["stable_owned_tags_reused"] = true
	return nil
}

func (run *acceptanceRun) checkedScenario(action func() error) error {
	if err := run.verifyPreserved(); err != nil {
		return err
	}
	if err := action(); err != nil {
		return err
	}
	return run.verifyPreserved()
}

func (run *acceptanceRun) optionDigests() (map[string][32]byte, error) {
	var entries []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := run.remote(0, http.MethodGet, "/api/option/", nil, &entries); err != nil {
		return nil, err
	}
	result := make(map[string][32]byte, len(entries))
	for _, entry := range entries {
		if entry.Key != "GroupRatio" && entry.Key != "UserUsableGroups" {
			result[entry.Key] = sha256.Sum256([]byte(entry.Value))
		}
	}
	return result, nil
}
