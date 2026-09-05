package reconciliation

import (
	"slices"
	"sort"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/shopspring/decimal"
)

func LocateManagedChannel(expected routing.DesiredChannel, binding *int64, channels []ActualChannel) (*ActualChannel, error) {
	matchingTag := make([]ActualChannel, 0, 1)
	var matchingID *ActualChannel
	for index := range channels {
		channel := channels[index]
		if channel.ManagedTag == expected.ManagedTag {
			matchingTag = append(matchingTag, channel)
		}
		if binding != nil && channel.ID == *binding {
			copyOfChannel := channel
			matchingID = &copyOfChannel
		}
	}
	if len(matchingTag) > 1 {
		return nil, failuref(FailureOwnership, "managed_resource_conflict", "managed tag %q belongs to multiple channels", expected.ManagedTag)
	}
	if matchingID != nil {
		if matchingID.ManagedTag != expected.ManagedTag {
			return nil, failuref(FailureOwnership, "binding_mismatch", "bound channel %d has a different managed tag", matchingID.ID)
		}
		return matchingID, nil
	}
	if len(matchingTag) == 1 {
		return &matchingTag[0], nil
	}
	return nil, nil
}

func MergeGroupRatios(current map[string]string, groupKey, expectedRatio string) (map[string]string, bool, error) {
	expected, err := decimal.NewFromString(expectedRatio)
	if err != nil || !expected.IsPositive() {
		return nil, false, failuref(FailureConfiguration, "invalid_sale_ratio", "sale ratio %q is invalid", expectedRatio)
	}
	merged := make(map[string]string, len(current)+1)
	for key, raw := range current {
		ratio, parseErr := decimal.NewFromString(raw)
		if parseErr != nil || ratio.IsNegative() {
			return nil, false, failuref(FailureCompatibility, "invalid_group_ratio", "New API group %q has an invalid ratio", key)
		}
		merged[key] = ratio.String()
	}
	normalized := expected.String()
	changed := merged[groupKey] != normalized
	merged[groupKey] = normalized
	return merged, changed, nil
}

func MergeUserUsableGroups(current map[string]string, group routing.DesiredGroup) (map[string]string, bool) {
	merged := make(map[string]string, len(current)+1)
	for key, description := range current {
		merged[key] = description
	}
	if !group.Visible {
		_, changed := merged[group.Key]
		delete(merged, group.Key)
		return merged, changed
	}
	changed := merged[group.Key] != group.DisplayName
	merged[group.Key] = group.DisplayName
	return merged, changed
}

func ChannelConfigurationMatches(expected routing.DesiredChannel, actual ActualChannel) bool {
	if expected.ManagedTag != actual.ManagedTag || expected.Name != actual.Name || expected.Protocol != actual.Protocol || expected.BaseURL != actual.BaseURL {
		return false
	}
	if expected.Priority != actual.Priority || expected.Weight != actual.Weight {
		return false
	}
	expectedModels := make([]string, 0, len(expected.Models))
	expectedMapping := make(map[string]string)
	for _, model := range expected.Models {
		expectedModels = append(expectedModels, model.Model)
		if model.Model != model.UpstreamModel {
			expectedMapping[model.Model] = model.UpstreamModel
		}
	}
	sort.Strings(expectedModels)
	actualModels := append([]string(nil), actual.Models...)
	sort.Strings(actualModels)
	if !slices.Equal(expectedModels, actualModels) || !mapsEqual(expectedMapping, actual.ModelMapping) {
		return false
	}
	expectedGroups := expected.GroupKeys()
	actualGroups := append([]string(nil), actual.Groups...)
	sort.Strings(actualGroups)
	return slices.Equal(expectedGroups, actualGroups)
}

func Verify(expected routing.Snapshot, actual ActualState, binding *int64) (ActualChannel, error) {
	channel, err := LocateManagedChannel(expected.Channel, binding, actual.Channels)
	if err != nil {
		return ActualChannel{}, err
	}
	if channel == nil {
		return ActualChannel{}, failuref(FailureCompatibility, "channel_missing", "managed channel is missing after synchronization")
	}
	if !ChannelConfigurationMatches(expected.Channel, *channel) {
		return ActualChannel{}, failuref(FailureCompatibility, "channel_drift", "managed channel configuration does not match the route plan")
	}
	ratio, ok := actual.GroupRatios[expected.Group.Key]
	if !ok {
		return ActualChannel{}, failuref(FailureCompatibility, "group_ratio_missing", "managed group ratio is missing after synchronization")
	}
	want, wantErr := decimal.NewFromString(expected.Group.SaleRatio)
	got, gotErr := decimal.NewFromString(ratio)
	if wantErr != nil || gotErr != nil || !want.Equal(got) {
		return ActualChannel{}, failuref(FailureCompatibility, "group_ratio_drift", "managed group ratio does not match the route plan")
	}
	if channel.Status != ChannelEnabled {
		return ActualChannel{}, failuref(FailureCompatibility, "channel_not_enabled", "managed channel is not enabled after synchronization")
	}
	description, visible := actual.UserUsableGroups[expected.Group.Key]
	if expected.Group.Visible && (!visible || description != expected.Group.DisplayName) {
		return ActualChannel{}, failuref(FailureCompatibility, "group_visibility_drift", "managed group is not user selectable after synchronization")
	}
	if !expected.Group.Visible && visible {
		return ActualChannel{}, failuref(FailureCompatibility, "group_visibility_drift", "hidden managed group remains user selectable after synchronization")
	}
	return *channel, nil
}

func VerifyUnmanagedGroupRatios(before, after map[string]string, managedGroup string) error {
	for key, rawBefore := range before {
		if key == managedGroup {
			continue
		}
		rawAfter, ok := after[key]
		if !ok {
			return failuref(FailureOwnership, "unmanaged_group_removed", "New API group %q disappeared during synchronization", key)
		}
		beforeRatio, beforeErr := decimal.NewFromString(rawBefore)
		afterRatio, afterErr := decimal.NewFromString(rawAfter)
		if beforeErr != nil || afterErr != nil || !beforeRatio.Equal(afterRatio) {
			return failuref(FailureOwnership, "unmanaged_group_changed", "New API group %q changed during synchronization", key)
		}
	}
	return nil
}

func VerifyUnmanagedUserGroups(before, after map[string]string, managedGroup string) error {
	for key, description := range before {
		if key == managedGroup {
			continue
		}
		actualDescription, ok := after[key]
		if !ok {
			return failuref(FailureOwnership, "unmanaged_user_group_removed", "New API user group %q disappeared during synchronization", key)
		}
		if actualDescription != description {
			return failuref(FailureOwnership, "unmanaged_user_group_changed", "New API user group %q changed during synchronization", key)
		}
	}
	return nil
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func Summary(state ActualState, tag, groupKey string) map[string]any {
	result := map[string]any{"version": state.Version}
	if ratio, ok := state.GroupRatios[groupKey]; ok {
		result["group_ratio"] = ratio
	}
	if description, ok := state.UserUsableGroups[groupKey]; ok {
		result["user_group_description"] = description
	}
	channels := make([]map[string]any, 0, 1)
	for _, channel := range state.Channels {
		if channel.ManagedTag == tag {
			channels = append(channels, map[string]any{
				"id":     channel.ID,
				"status": channel.Status,
				"models": channel.Models,
				"groups": channel.Groups,
			})
		}
	}
	result["channels"] = channels
	return result
}
