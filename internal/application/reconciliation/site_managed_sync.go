package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

func (s *Service) tryRunManagedSite(
	ctx context.Context,
	store SiteStore,
	gateway Gateway,
	bundle Bundle,
) (bool, error) {
	managed, ok := gateway.(ManagedSyncGateway)
	if !ok {
		return false, nil
	}
	capabilities, err := managed.ReadManagedSyncCapabilities(ctx)
	if err != nil {
		if managedSyncLegacyFallback(err) {
			return false, nil
		}
		return true, s.finishFailure(ctx, bundle, "managed_sync_capabilities", err)
	}
	if err := validateManagedSyncCapabilities(capabilities, bundle.Plan.Snapshot); err != nil {
		return true, s.finishFailure(ctx, bundle, "managed_sync_capabilities", err)
	}
	if approvals, ok := s.store.(ManagedSyncApprovalStore); ok {
		approved, err := approvals.ManagedSyncApproved(ctx, bundle.Site.ID, capabilities)
		if err != nil {
			return true, s.finishFailure(ctx, bundle, "managed_sync_approval", err)
		}
		if !approved {
			return true, s.finishFailure(ctx, bundle, "managed_sync_approval", NewFailure(
				FailureCompatibility,
				"managed_sync_unapproved",
				"New API build has not passed the current compatibility catalog",
				nil,
			))
		}
	}
	return true, s.runManagedSite(ctx, store, managed, capabilities, bundle)
}

func (s *Service) runManagedSite(
	ctx context.Context,
	store SiteStore,
	gateway ManagedSyncGateway,
	capabilities ManagedSyncCapabilities,
	bundle Bundle,
) error {
	before, err := gateway.ReadManagedState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "managed_sync_read", err)
	}
	before.Actual.Version = capabilities.NewAPIVersion
	if len(before.Conflicts) > 0 {
		return s.finishFailure(ctx, bundle, "managed_sync_ownership", NewFailure(
			FailureOwnership,
			"managed_resource_conflict",
			"New API reported conflicting managed resources",
			errors.New(strings.Join(before.Conflicts, "; ")),
		))
	}
	if before.BillingBasisHash != bundle.Plan.Snapshot.BillingBasisHash {
		return s.finishFailure(ctx, bundle, "managed_sync_billing_basis", NewFailure(
			FailureConfiguration,
			"billing_basis_changed",
			"New API model pricing changed; publish reviewed prices before continuing",
			nil,
		))
	}

	request := ManagedSyncRequest{
		OperationID: bundle.Operation.ID, RoutePlanVersion: bundle.Plan.Version,
		ExpectedStateHash: before.StateHash, Groups: bundle.Plan.Snapshot.Groups(),
		Channels: make([]ManagedSyncChannel, 0, len(bundle.Resources)),
	}
	secrets := make([][]byte, 0, len(bundle.Resources))
	defer func() {
		for _, secret := range secrets {
			clear(secret)
		}
	}()
	for _, resource := range bundle.Resources {
		desired := resource.Snapshot.Channel
		if desired.DesiredStatus == routing.DesiredEnabled && !resource.CredentialAvailable {
			return s.finishResourceFailure(ctx, bundle, resource, "managed_sync_credential", NewFailure(
				FailureAuthentication,
				"credential_unavailable",
				"the route plan supplier credential is missing or revoked",
				nil,
			))
		}
		var secret []byte
		if resource.CredentialAvailable {
			secret, err = s.vault.Decrypt(resource.SupplierCredential)
			if err != nil {
				return s.finishResourceFailure(ctx, bundle, resource, "managed_sync_credential", NewFailure(
					FailureAuthentication,
					"credential_unavailable",
					"supplier credential could not be decrypted",
					err,
				))
			}
			secrets = append(secrets, secret)
		}
		request.Channels = append(request.Channels, ManagedSyncChannel{
			Desired: desired,
			APIKey:  secret,
			Resume:  slices.Contains(bundle.Plan.Snapshot.ResumeRelationIDs, resource.Snapshot.RelationID),
		})
	}

	result, err := gateway.ApplyManagedState(ctx, request)
	if err != nil {
		return s.finishFailure(ctx, bundle, "managed_sync_apply", err)
	}
	result.State.Actual.Version = capabilities.NewAPIVersion
	if len(result.State.Conflicts) > 0 {
		return s.finishFailure(ctx, bundle, "managed_sync_verify", NewFailure(
			FailureOwnership,
			"managed_resource_conflict",
			"New API reported conflicting managed resources after synchronization",
			errors.New(strings.Join(result.State.Conflicts, "; ")),
		))
	}
	if result.State.BillingBasisHash != bundle.Plan.Snapshot.BillingBasisHash {
		return s.finishFailure(ctx, bundle, "managed_sync_verify_basis", NewFailure(
			FailureConfiguration,
			"billing_basis_changed",
			"New API model pricing changed during synchronization",
			nil,
		))
	}
	if err := verifySiteState(bundle, before.Actual, result.State.Actual); err != nil {
		return s.finishFailure(ctx, bundle, "managed_sync_verify", err)
	}

	beforeByTag := make(map[string]ActualChannel, len(before.Actual.Channels))
	for _, channel := range before.Actual.Channels {
		beforeByTag[channel.ManagedTag] = channel
	}
	now := s.now().UTC()
	for _, resource := range bundle.Resources {
		channel, err := LocateManagedChannel(
			resource.Snapshot.Channel,
			resource.ManagedChannel.ExternalChannelID,
			result.State.Actual.Channels,
		)
		if err != nil {
			return s.finishResourceFailure(ctx, bundle, resource, "managed_sync_confirm", err)
		}
		var externalID *int64
		if channel != nil {
			value := channel.ID
			externalID = &value
		}
		previous, existed := beforeByTag[resource.Snapshot.Channel.ManagedTag]
		credentialApplied := resource.CredentialAvailable && (!existed ||
			previous.CredentialVersion != resource.Snapshot.Channel.CredentialVersion)
		if err := store.ConfirmResource(ctx, bundle, ResourceConfirmation{
			Resource: resource, ExternalChannelID: externalID, CredentialApplied: credentialApplied,
		}, now); err != nil {
			return fmt.Errorf("confirm managed sync resource: %w", err)
		}
		if err := s.recordSuccess(ctx, bundle.Operation.ID, resourceStep(resource, "managed_batch_confirmed"),
			previous, channel, now); err != nil {
			return err
		}
	}
	if err := store.ConfirmSitePrices(ctx, bundle, now); err != nil {
		return fmt.Errorf("confirm managed sync prices: %w", err)
	}
	if err := s.recordSuccess(ctx, bundle.Operation.ID, "managed_batch_applied", before.StateHash, map[string]any{
		"state_hash": result.State.StateHash, "actions": len(result.Actions), "replayed": result.Replayed,
	}, now); err != nil {
		return err
	}
	if err := store.CompleteSiteOperation(ctx, bundle, now); err != nil {
		return fmt.Errorf("confirm managed sync completion: %w", err)
	}
	return nil
}

func validateManagedSyncCapabilities(capabilities ManagedSyncCapabilities, snapshot routing.Snapshot) error {
	features := capabilities.Features
	if capabilities.ContractVersion != ManagedSyncContractVersion || capabilities.NewAPIVersion == "" ||
		!features.AtomicApply || !features.ManagedChannels || !features.MultipleGroups || !features.GroupRatios ||
		!features.EntryVisibility || !features.PersistentIdempotency || !features.FinalStateDigest || !features.LogRead {
		return NewFailure(FailureCompatibility, "managed_sync_incompatible", "New API managed sync capabilities are incomplete", nil)
	}
	if capabilities.DatabaseType != "sqlite" && capabilities.DatabaseType != "mysql" && capabilities.DatabaseType != "postgres" {
		return NewFailure(FailureCompatibility, "managed_sync_database", "New API managed sync database type is unsupported", nil)
	}
	groups := snapshot.Groups()
	modelCount := 0
	maxGroupBytes := 0
	for _, resource := range snapshot.Resources {
		modelCount += len(resource.Channel.Models)
		for _, group := range resource.Channel.GroupKeys() {
			maxGroupBytes = max(maxGroupBytes, len(group))
		}
	}
	for _, group := range groups {
		maxGroupBytes = max(maxGroupBytes, len(group.Key))
	}
	limits := capabilities.Limits
	if limits.MaxChannels < len(snapshot.Resources) || limits.MaxGroups < len(groups) ||
		limits.MaxModels < modelCount || limits.MaxGroupKeyBytes < maxGroupBytes || limits.MaxRequestBytes <= 0 {
		return NewFailure(FailureCompatibility, "managed_sync_limits", "New API managed sync limits are too small for the site plan", nil)
	}
	if len(capabilities.BillingBasisHash) != 64 || len(capabilities.BillingBasis) == 0 {
		return NewFailure(FailureCompatibility, "managed_sync_billing_basis", "New API managed sync did not expose a pricing basis", nil)
	}
	return nil
}

func managedSyncLegacyFallback(err error) bool {
	var failure *Failure
	if !errors.As(err, &failure) {
		return false
	}
	if failure.Kind == FailureAuthentication {
		return true
	}
	return failure.Code == "gateway_http_404" || failure.Code == "gateway_http_503"
}
