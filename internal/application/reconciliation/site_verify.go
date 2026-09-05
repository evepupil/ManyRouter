package reconciliation

import (
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/shopspring/decimal"
)

func verifySiteGroups(groups []routing.DesiredGroup, actual ActualState, visibility bool) error {
	for _, group := range groups {
		want, wantErr := decimal.NewFromString(group.SaleRatio)
		got, gotErr := decimal.NewFromString(actual.GroupRatios[group.Key])
		if wantErr != nil || gotErr != nil || !want.Equal(got) {
			return NewFailure(FailureCompatibility, "group_ratio_drift", "managed group price does not match the site plan", nil)
		}
		if visibility {
			description, visible := actual.UserUsableGroups[group.Key]
			if group.Visible != visible || (visible && description != group.DisplayName) {
				return NewFailure(FailureCompatibility, "group_visibility_drift", "managed group visibility does not match the site plan", nil)
			}
		}
	}
	return nil
}

func verifySiteState(bundle Bundle, before, after ActualState) error {
	groups := bundle.Plan.Snapshot.Groups()
	if err := verifySiteGroups(groups, after, true); err != nil {
		return err
	}
	beforeRatios, beforeGroups := copyStringMap(before.GroupRatios), copyStringMap(before.UserUsableGroups)
	for _, group := range groups {
		delete(beforeRatios, group.Key)
		delete(beforeGroups, group.Key)
	}
	if err := VerifyUnmanagedGroupRatios(beforeRatios, after.GroupRatios, ""); err != nil {
		return err
	}
	if err := VerifyUnmanagedUserGroups(beforeGroups, after.UserUsableGroups, ""); err != nil {
		return err
	}
	for _, resource := range bundle.Resources {
		channel, err := LocateManagedChannel(resource.Snapshot.Channel, resource.ManagedChannel.ExternalChannelID, after.Channels)
		if err != nil {
			return err
		}
		if resource.Snapshot.Channel.DesiredStatus == routing.DesiredDisabled {
			if channel != nil && channel.Status != ChannelManuallyDisabled {
				return NewFailure(FailureCompatibility, "channel_not_disabled", "managed channel remains active after shutdown", nil)
			}
			continue
		}
		if channel == nil || channel.Status != ChannelEnabled || !ChannelConfigurationMatches(resource.Snapshot.Channel, *channel) {
			return NewFailure(FailureCompatibility, "channel_drift", "managed channel does not match the complete site plan", nil)
		}
	}
	return nil
}
