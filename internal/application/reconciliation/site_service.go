package reconciliation

import (
	"context"
	"errors"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

func (s *Service) newGateway(bundle Bundle, adminSecret []byte) (Gateway, error) {
	if factory, ok := s.gateways.(SiteGatewayFactory); ok {
		userID := bundle.Site.AdminUserID
		if userID == 0 {
			userID = 1
		}
		return factory.NewForSite(bundle.Site.NewAPIBaseURL, adminSecret, userID)
	}
	return s.gateways.New(bundle.Site.NewAPIBaseURL, adminSecret)
}

func (s *Service) runSite(ctx context.Context, bundle Bundle) error {
	store, ok := s.store.(SiteStore)
	if !ok {
		return errors.New("site synchronization persistence is unavailable")
	}
	if len(bundle.Resources) != len(bundle.Plan.Snapshot.Resources) {
		return s.finishFailure(ctx, bundle, "load_resources", NewFailure(FailureConfiguration, "site_resources_missing", "site synchronization resources are incomplete", nil))
	}
	if err := s.store.StartOperation(ctx, bundle.Operation, "probe", s.now().UTC()); err != nil {
		return err
	}
	bundle.Operation.Attempt++
	adminSecret, err := s.vault.Decrypt(bundle.AdminCredential)
	if err != nil {
		return s.finishFailure(ctx, bundle, "decrypt_admin", NewFailure(FailureAuthentication, "credential_unavailable", "New API management credential is unavailable", err))
	}
	defer clear(adminSecret)
	gateway, err := s.newGateway(bundle, adminSecret)
	if err != nil {
		return s.finishFailure(ctx, bundle, "create_gateway", err)
	}
	if handled, err := s.tryRunManagedSite(ctx, store, gateway, bundle); handled {
		return err
	}
	actual, err := gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "read_actual", err)
	}
	for _, resource := range bundle.Resources {
		if _, err := LocateManagedChannel(resource.Snapshot.Channel, resource.ManagedChannel.ExternalChannelID, actual.Channels); err != nil {
			return s.finishResourceFailure(ctx, bundle, resource, "ownership", err)
		}
	}
	baseline := actual
	// Safety shutdowns do not depend on upstream credentials, pricing, or model availability.
	for index := range bundle.Resources {
		resource := &bundle.Resources[index]
		if resource.Snapshot.Channel.DesiredStatus != routing.DesiredDisabled {
			continue
		}
		if err := s.disableResource(ctx, store, gateway, bundle, resource, actual); err != nil {
			return s.finishResourceFailure(ctx, bundle, *resource, "disable", err)
		}
	}
	if err := verifyBillingBasis(ctx, gateway, bundle.Plan.Snapshot.BillingBasisHash); err != nil {
		return s.finishFailure(ctx, bundle, "verify_billing_basis", err)
	}
	actual, err = gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "read_before_groups", err)
	}
	ratios := actual.GroupRatios
	changed := false
	for _, group := range bundle.Plan.Snapshot.Groups() {
		var groupChanged bool
		ratios, groupChanged, err = MergeGroupRatios(ratios, group.Key, group.SaleRatio)
		if err != nil {
			return s.finishFailure(ctx, bundle, "plan_group_prices", err)
		}
		changed = changed || groupChanged
	}
	if changed {
		if err := gateway.SetGroupRatios(ctx, ratios); err != nil {
			return s.finishFailure(ctx, bundle, "set_group_prices", err)
		}
	}
	actual, err = gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "read_group_prices", err)
	}
	if err := verifySiteGroups(bundle.Plan.Snapshot.Groups(), actual, false); err != nil {
		return s.finishFailure(ctx, bundle, "verify_group_prices", err)
	}
	if err := verifyBillingBasis(ctx, gateway, bundle.Plan.Snapshot.BillingBasisHash); err != nil {
		return s.finishFailure(ctx, bundle, "verify_group_price_basis", err)
	}
	if err := store.ConfirmSitePrices(ctx, bundle, s.now().UTC()); err != nil {
		return fmt.Errorf("confirm site prices: %w", err)
	}
	for index := range bundle.Resources {
		resource := &bundle.Resources[index]
		if resource.Snapshot.Channel.DesiredStatus == routing.DesiredDisabled {
			desired := resource.Snapshot.Channel
			credentialChanged := resource.ManagedChannel.LastConfirmedCredentialID != desired.CredentialID || resource.ManagedChannel.LastConfirmedCredentialVersion != desired.CredentialVersion
			if resource.ManagedChannel.ExternalChannelID == nil || !credentialChanged || (resource.ManagedChannel.LastConfirmedCredentialVersion == 0 && desired.CredentialVersion == 1) {
				continue
			}
		}
		if err := s.applyResource(ctx, store, gateway, bundle, resource); err != nil {
			return s.finishResourceFailure(ctx, bundle, *resource, "apply", err)
		}
	}
	actual, err = gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "read_before_visibility", err)
	}
	userGroups := actual.UserUsableGroups
	changed = false
	for _, group := range bundle.Plan.Snapshot.Groups() {
		var groupChanged bool
		userGroups, groupChanged = MergeUserUsableGroups(userGroups, group)
		changed = changed || groupChanged
	}
	if changed {
		if err := gateway.SetUserUsableGroups(ctx, userGroups); err != nil {
			return s.finishFailure(ctx, bundle, "set_group_visibility", err)
		}
	}
	finalState, err := gateway.ReadActualState(ctx)
	if err != nil {
		return s.finishFailure(ctx, bundle, "final_read", err)
	}
	if err := verifySiteState(bundle, baseline, finalState); err != nil {
		return s.finishFailure(ctx, bundle, "verify_site", err)
	}
	if err := verifyBillingBasis(ctx, gateway, bundle.Plan.Snapshot.BillingBasisHash); err != nil {
		return s.finishFailure(ctx, bundle, "verify_final_billing_basis", err)
	}
	if err := store.CompleteSiteOperation(ctx, bundle, s.now().UTC()); err != nil {
		return fmt.Errorf("confirm complete site synchronization: %w", err)
	}
	return nil
}

func (s *Service) finishResourceFailure(ctx context.Context, bundle Bundle, resource ResourceBundle, action string, err error) error {
	bundle.Operation.RelationID = resource.Snapshot.RelationID
	return s.finishFailure(ctx, bundle, resourceStep(resource, action), err)
}

func verifyBillingBasis(ctx context.Context, gateway Gateway, expected string) error {
	reader, ok := gateway.(BillingBasisReader)
	if !ok {
		return NewFailure(FailureCompatibility, "billing_basis_unavailable", "New API pricing settings cannot be verified", nil)
	}
	_, actual, err := reader.ReadBillingBasis(ctx)
	if err != nil {
		return err
	}
	if actual != expected {
		return NewFailure(FailureConfiguration, "billing_basis_changed", "New API model pricing changed; publish reviewed prices before continuing", nil)
	}
	return nil
}

func resourceStep(resource ResourceBundle, action string) string {
	return resource.Snapshot.RelationID.String() + ":" + action
}
