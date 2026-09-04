package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

type Service struct {
	store      Store
	vault      CredentialVault
	gateways   GatewayFactory
	dispatcher Dispatcher
	now        func() time.Time
	newID      func() uuid.UUID
}

func NewService(
	store Store,
	vault CredentialVault,
	gateways GatewayFactory,
	dispatcher Dispatcher,
	now func() time.Time,
	newID func() uuid.UUID,
) (*Service, error) {
	if store == nil || vault == nil || gateways == nil || now == nil || newID == nil {
		return nil, errors.New("reconciliation dependencies are required")
	}
	return &Service{store: store, vault: vault, gateways: gateways, dispatcher: dispatcher, now: now, newID: newID}, nil
}

func (s *Service) RequestSync(ctx context.Context, relationID uuid.UUID) (Operation, error) {
	operation, err := s.store.CreateOperation(ctx, relationID, s.newID(), s.now().UTC())
	if err != nil {
		return Operation{}, fmt.Errorf("create synchronization operation: %w", err)
	}
	if operation.Status == OperationSucceeded || operation.Status == OperationManualRequired {
		return operation, nil
	}
	if s.dispatcher != nil {
		if err := s.dispatcher.Dispatch(ctx, operation.ID); err != nil {
			return Operation{}, fmt.Errorf("dispatch synchronization operation: %w", err)
		}
	}
	return operation, nil
}

func (s *Service) GetOperation(ctx context.Context, id uuid.UUID) (Operation, error) {
	return s.store.GetOperation(ctx, id)
}

func (s *Service) Run(ctx context.Context, operationID uuid.UUID) (runErr error) {
	bundle, err := s.store.LoadBundle(ctx, operationID)
	if err != nil {
		return fmt.Errorf("load synchronization operation: %w", err)
	}
	if bundle.Operation.Status == OperationSucceeded || bundle.Operation.Status == OperationManualRequired {
		return nil
	}
	if err := bundle.Site.CanSync(); err != nil {
		kind := FailureConfiguration
		if bundle.Site.CompatibilityStatus == site.CompatibilityIncompatible {
			kind = FailureCompatibility
		}
		return s.finishFailure(ctx, bundle, "validate_site", NewFailure(kind, "site_not_writable", "site is not available for synchronization", err))
	}
	lock, acquired, err := s.store.AcquireSiteLock(ctx, bundle.Site.ID)
	if err != nil {
		return fmt.Errorf("acquire site synchronization lock: %w", err)
	}
	if !acquired {
		return NewFailure(FailureRetryable, "site_sync_busy", "another synchronization is running for this site", nil)
	}
	defer func() {
		releaseContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lock.Release(releaseContext); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("release site synchronization lock: %w", err))
		}
	}()

	startedAt := s.now().UTC()
	if err := s.store.StartOperation(ctx, bundle.Operation, "probe", startedAt); err != nil {
		return fmt.Errorf("start synchronization operation: %w", err)
	}
	bundle.Operation.Attempt++

	adminSecret, err := s.vault.Decrypt(bundle.AdminCredential)
	if err != nil {
		return s.finishFailure(ctx, bundle, "decrypt_credentials", NewFailure(FailureAuthentication, "credential_unavailable", "New API credential could not be decrypted", err))
	}
	defer clear(adminSecret)
	supplierSecret, err := s.vault.Decrypt(bundle.SupplierCredential)
	if err != nil {
		return s.finishFailure(ctx, bundle, "decrypt_credentials", NewFailure(FailureAuthentication, "credential_unavailable", "supplier credential could not be decrypted", err))
	}
	defer clear(supplierSecret)

	gateway, err := s.gateways.New(bundle.Site.NewAPIBaseURL, adminSecret)
	if err != nil {
		return s.finishFailure(ctx, bundle, "create_gateway", NewFailure(FailureConfiguration, "gateway_configuration", "New API client could not be created", err))
	}
	version, err := gateway.Probe(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "probe", err)
	}
	if err := s.recordSuccess(ctx, operationID, "probe", nil, map[string]any{"version": version}, startedAt); err != nil {
		return err
	}

	actual, err := gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "read_actual", err)
	}
	readAt := s.now().UTC()
	if err := s.recordSuccess(ctx, operationID, "read_actual", nil, Summary(actual, bundle.Plan.Snapshot.Channel.ManagedTag, bundle.Plan.Snapshot.Group.Key), readAt); err != nil {
		return err
	}
	unmanagedGroupBaseline := copyStringMap(actual.GroupRatios)
	unmanagedUserGroupBaseline := copyStringMap(actual.UserUsableGroups)

	binding := bundle.ManagedChannel.ExternalChannelID
	actualChannel, err := LocateManagedChannel(bundle.Plan.Snapshot.Channel, binding, actual.Channels)
	if err != nil {
		return s.finishFailure(ctx, bundle, "locate_channel", err)
	}
	if actualChannel != nil && actualChannel.Status == ChannelManuallyDisabled && bundle.ManagedChannel.LastConfirmedPlanVersion != nil {
		return s.finishFailure(ctx, bundle, "manual_lock", NewFailure(FailureManualLock, "channel_manually_disabled", "managed channel was manually disabled in New API", nil))
	}

	mergedRatios, ratioChanged, err := MergeGroupRatios(actual.GroupRatios, bundle.Plan.Snapshot.Group.Key, bundle.Plan.Snapshot.Group.SaleRatio)
	if err != nil {
		return s.finishFailure(ctx, bundle, "merge_group_ratio", err)
	}
	if ratioChanged {
		stepStarted := s.now().UTC()
		if err := gateway.SetGroupRatios(ctx, mergedRatios); err != nil {
			return s.finishFailure(ctx, bundle, "set_group_ratio", err)
		}
		if err := s.recordSuccess(ctx, operationID, "set_group_ratio", map[string]any{"ratio": actual.GroupRatios[bundle.Plan.Snapshot.Group.Key]}, map[string]any{"ratio": bundle.Plan.Snapshot.Group.SaleRatio}, stepStarted); err != nil {
			return err
		}
	}
	mergedUserGroups, visibilityChanged := MergeUserUsableGroups(actual.UserUsableGroups, bundle.Plan.Snapshot.Group)
	if visibilityChanged {
		stepStarted := s.now().UTC()
		if err := gateway.SetUserUsableGroups(ctx, mergedUserGroups); err != nil {
			return s.finishFailure(ctx, bundle, "set_group_visibility", err)
		}
		if err := s.recordSuccess(ctx, operationID, "set_group_visibility", nil, map[string]any{
			"group_key": bundle.Plan.Snapshot.Group.Key, "visible": bundle.Plan.Snapshot.Group.Visible,
		}, stepStarted); err != nil {
			return err
		}
	}

	created := false
	if actualChannel == nil {
		stepStarted := s.now().UTC()
		if err := gateway.CreateChannel(ctx, bundle.Plan.Snapshot.Channel, supplierSecret); err != nil {
			return s.finishFailure(ctx, bundle, "create_channel", err)
		}
		created = true
		if err := s.recordSuccess(ctx, operationID, "create_channel", nil, map[string]any{"managed_tag": bundle.Plan.Snapshot.Channel.ManagedTag}, stepStarted); err != nil {
			return err
		}
	} else if !ChannelConfigurationMatches(bundle.Plan.Snapshot.Channel, *actualChannel) {
		stepStarted := s.now().UTC()
		if err := gateway.UpdateChannel(ctx, actualChannel.ID, bundle.Plan.Snapshot.Channel, supplierSecret); err != nil {
			return s.finishFailure(ctx, bundle, "update_channel", err)
		}
		if err := s.recordSuccess(ctx, operationID, "update_channel", map[string]any{"channel_id": actualChannel.ID}, map[string]any{"managed_tag": bundle.Plan.Snapshot.Channel.ManagedTag}, stepStarted); err != nil {
			return err
		}
	}

	if created || actualChannel == nil || !ChannelConfigurationMatches(bundle.Plan.Snapshot.Channel, *actualChannel) {
		actual, err = gateway.ReadActualState(ctx)
		if err != nil {
			return s.finishFailure(ctx, bundle, "read_after_channel_write", err)
		}
		actualChannel, err = LocateManagedChannel(bundle.Plan.Snapshot.Channel, binding, actual.Channels)
		if err != nil {
			return s.finishFailure(ctx, bundle, "locate_channel_after_write", err)
		}
		if actualChannel == nil {
			return s.finishFailure(ctx, bundle, "locate_channel_after_write", NewFailure(FailureUncertain, "channel_create_unconfirmed", "created channel was not visible during verification", nil))
		}
	}

	if binding == nil || *binding != actualChannel.ID {
		if err := s.store.BindChannel(ctx, bundle.ManagedChannel.ID, actualChannel.ID, s.now().UTC()); err != nil {
			return fmt.Errorf("save New API channel binding: %w", err)
		}
		binding = &actualChannel.ID
	}

	testModel := bundle.Plan.Snapshot.Channel.Models[0].Model
	testStarted := s.now().UTC()
	if err := gateway.TestChannel(ctx, actualChannel.ID, testModel); err != nil {
		return s.finishFailure(ctx, bundle, "test_channel", err)
	}
	if err := s.recordSuccess(ctx, operationID, "test_channel", nil, map[string]any{"channel_id": actualChannel.ID, "model": testModel}, testStarted); err != nil {
		return err
	}

	if actualChannel.Status != ChannelEnabled {
		enableStarted := s.now().UTC()
		if err := gateway.SetChannelEnabled(ctx, actualChannel.ID, true); err != nil {
			return s.finishFailure(ctx, bundle, "enable_channel", err)
		}
		if err := s.recordSuccess(ctx, operationID, "enable_channel", map[string]any{"status": actualChannel.Status}, map[string]any{"status": ChannelEnabled}, enableStarted); err != nil {
			return err
		}
	}

	finalState, err := gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "final_read", err)
	}
	if err := VerifyUnmanagedGroupRatios(unmanagedGroupBaseline, finalState.GroupRatios, bundle.Plan.Snapshot.Group.Key); err != nil {
		return s.finishFailure(ctx, bundle, "verify_unmanaged_groups", err)
	}
	if err := VerifyUnmanagedUserGroups(unmanagedUserGroupBaseline, finalState.UserUsableGroups, bundle.Plan.Snapshot.Group.Key); err != nil {
		return s.finishFailure(ctx, bundle, "verify_unmanaged_user_groups", err)
	}
	verifiedChannel, err := Verify(bundle.Plan.Snapshot, finalState, binding)
	if err != nil {
		return s.finishFailure(ctx, bundle, "verify", err)
	}
	verifyStarted := s.now().UTC()
	if err := s.recordSuccess(ctx, operationID, "verify", nil, Summary(finalState, bundle.Plan.Snapshot.Channel.ManagedTag, bundle.Plan.Snapshot.Group.Key), verifyStarted); err != nil {
		return err
	}
	if err := s.store.CompleteOperation(ctx, bundle, verifiedChannel.ID, s.now().UTC()); err != nil {
		return fmt.Errorf("complete synchronization operation: %w", err)
	}
	return nil
}
