package reconciliation

import (
	"context"
	"slices"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

func (s *Service) disableResource(ctx context.Context, store SiteStore, gateway Gateway, bundle Bundle, resource *ResourceBundle, actual ActualState) error {
	channel, err := LocateManagedChannel(resource.Snapshot.Channel, resource.ManagedChannel.ExternalChannelID, actual.Channels)
	if err != nil {
		return err
	}
	var externalID *int64
	if channel != nil {
		externalID = &channel.ID
		if channel.Status != ChannelManuallyDisabled {
			if err := gateway.SetChannelEnabled(ctx, channel.ID, false); err != nil {
				return err
			}
		}
		state, err := gateway.ReadActualState(ctx)
		if err != nil {
			return err
		}
		verified, err := LocateManagedChannel(resource.Snapshot.Channel, externalID, state.Channels)
		if err != nil {
			return err
		}
		if verified == nil || verified.Status != ChannelManuallyDisabled {
			return NewFailure(FailureUncertain, "disable_unconfirmed", "managed channel shutdown has not been confirmed", nil)
		}
	}
	resource.ManagedChannel.ExternalChannelID = externalID
	if err := store.ConfirmResource(ctx, bundle, ResourceConfirmation{Resource: *resource, ExternalChannelID: externalID}, s.now().UTC()); err != nil {
		return err
	}
	return s.recordSuccess(ctx, bundle.Operation.ID, resourceStep(*resource, "disabled"), nil, map[string]any{"enabled": false}, s.now().UTC())
}

func (s *Service) applyResource(ctx context.Context, store SiteStore, gateway Gateway, bundle Bundle, resource *ResourceBundle) error {
	actual, err := gateway.ReadActualState(ctx)
	if err != nil {
		return err
	}
	desired := resource.Snapshot.Channel
	targetEnabled := desired.DesiredStatus == routing.DesiredEnabled
	if targetEnabled && !resource.CredentialAvailable {
		return NewFailure(FailureAuthentication, "credential_unavailable", "the route plan supplier credential is missing or revoked", nil)
	}
	channel, err := LocateManagedChannel(desired, resource.ManagedChannel.ExternalChannelID, actual.Channels)
	if err != nil {
		return err
	}
	resume := slices.Contains(bundle.Plan.Snapshot.ResumeRelationIDs, resource.Snapshot.RelationID)
	lastEnabled := resource.ManagedChannel.LastConfirmedEnabled
	if targetEnabled && channel != nil && channel.Status == ChannelManuallyDisabled && resource.ManagedChannel.LastConfirmedPlanVersion != nil && (lastEnabled == nil || *lastEnabled) && !resume {
		return NewFailure(FailureManualLock, "channel_manually_disabled", "managed channel was manually disabled in New API", nil)
	}
	credentialChanged := resource.ManagedChannel.LastConfirmedCredentialID != desired.CredentialID || resource.ManagedChannel.LastConfirmedCredentialVersion != desired.CredentialVersion
	configurationChanged := channel == nil || !ChannelConfigurationMatches(desired, *channel)
	needsWrite := configurationChanged || credentialChanged
	if needsWrite {
		if !resource.CredentialAvailable {
			return NewFailure(FailureAuthentication, "credential_unavailable", "the route plan supplier credential is missing or revoked", nil)
		}
		secret, err := s.vault.Decrypt(resource.SupplierCredential)
		if err != nil {
			return NewFailure(FailureAuthentication, "credential_unavailable", "supplier credential could not be decrypted", err)
		}
		defer clear(secret)
		if channel == nil {
			if err := gateway.CreateChannel(ctx, desired, secret); err != nil {
				return err
			}
		} else if err := gateway.UpdateChannel(ctx, channel.ID, desired, secret); err != nil {
			return err
		}
		actual, err = gateway.ReadActualState(ctx)
		if err != nil {
			return err
		}
		channel, err = LocateManagedChannel(desired, resource.ManagedChannel.ExternalChannelID, actual.Channels)
		if err != nil {
			return err
		}
		if channel == nil {
			return NewFailure(FailureUncertain, "channel_create_unconfirmed", "created channel was not visible during verification", nil)
		}
	}
	if channel == nil {
		return NewFailure(FailureCompatibility, "channel_missing", "managed channel is missing", nil)
	}
	if resource.ManagedChannel.ExternalChannelID == nil || *resource.ManagedChannel.ExternalChannelID != channel.ID {
		if err := s.store.BindChannel(ctx, resource.ManagedChannel.ID, channel.ID, s.now().UTC()); err != nil {
			return err
		}
		resource.ManagedChannel.ExternalChannelID = &channel.ID
	}
	if needsWrite || channel.Status != ChannelEnabled || resource.ManagedChannel.LastConfirmedPlanVersion == nil {
		testSecret, err := s.vault.Decrypt(resource.SupplierCredential)
		if err != nil {
			return NewFailure(FailureAuthentication, "credential_unavailable", "supplier credential could not be decrypted", err)
		}
		testErr := gateway.TestChannel(ctx, channel.ID, desired.Models[0].Model, testSecret)
		clear(testSecret)
		if testErr != nil {
			return testErr
		}
		// Legacy New API creates channels before refreshing its in-memory channel table.
		if resource.ManagedChannel.LastConfirmedPlanVersion == nil && channel.Status != ChannelEnabled {
			secret, err := s.vault.Decrypt(resource.SupplierCredential)
			if err != nil {
				return err
			}
			err = gateway.UpdateChannel(ctx, channel.ID, desired, secret)
			clear(secret)
			if err != nil {
				return err
			}
		}
	}
	if targetEnabled && channel.Status != ChannelEnabled {
		if err := gateway.SetChannelEnabled(ctx, channel.ID, true); err != nil {
			return err
		}
	}
	finalState, err := gateway.ReadActualState(ctx)
	if err != nil {
		return err
	}
	verified, err := LocateManagedChannel(desired, resource.ManagedChannel.ExternalChannelID, finalState.Channels)
	if err != nil {
		return err
	}
	expectedStatus := ChannelManuallyDisabled
	if targetEnabled {
		expectedStatus = ChannelEnabled
	}
	if verified == nil || verified.Status != expectedStatus || !ChannelConfigurationMatches(desired, *verified) {
		return NewFailure(FailureCompatibility, "channel_drift", "managed channel configuration does not match the site plan", nil)
	}
	confirmation := ResourceConfirmation{Resource: *resource, ExternalChannelID: &verified.ID, CredentialApplied: needsWrite}
	if err := store.ConfirmResource(ctx, bundle, confirmation, s.now().UTC()); err != nil {
		return err
	}
	return s.recordSuccess(ctx, bundle.Operation.ID, resourceStep(*resource, "confirmed"), nil, map[string]any{"channel_id": verified.ID, "enabled": targetEnabled, "credential_version": desired.CredentialVersion}, s.now().UTC())
}
