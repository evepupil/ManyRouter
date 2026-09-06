package reconciliation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

type fakeSiteStore struct {
	*fakeReconciliationStore
	confirmations   []reconciliation.ResourceConfirmation
	loads           int
	replaceOnReload uuid.UUID
	superseded      bool
	pricesConfirmed bool
}

func (s *fakeSiteStore) ConfirmSitePrices(context.Context, reconciliation.Bundle, time.Time) error {
	s.pricesConfirmed = true
	return nil
}

func (s *fakeSiteStore) LoadBundle(ctx context.Context, id uuid.UUID) (reconciliation.Bundle, error) {
	s.loads++
	if s.loads == 2 && s.replaceOnReload != uuid.Nil {
		s.bundle.CurrentPlanID = s.replaceOnReload
	}
	return s.fakeReconciliationStore.LoadBundle(ctx, id)
}

func (s *fakeSiteStore) SupersedeOperation(context.Context, reconciliation.Bundle, time.Time) error {
	s.superseded = true
	return nil
}

func (s *fakeSiteStore) ConfirmResource(_ context.Context, _ reconciliation.Bundle, confirmation reconciliation.ResourceConfirmation, _ time.Time) error {
	s.confirmations = append(s.confirmations, confirmation)
	for index := range s.bundle.Resources {
		resource := &s.bundle.Resources[index]
		if resource.Snapshot.RelationID != confirmation.Resource.Snapshot.RelationID {
			continue
		}
		resource.ManagedChannel.ExternalChannelID = confirmation.ExternalChannelID
		version := s.bundle.Plan.Version
		enabled := resource.Snapshot.Channel.DesiredStatus == routing.DesiredEnabled
		resource.ManagedChannel.LastConfirmedPlanVersion = &version
		resource.ManagedChannel.LastConfirmedEnabled = &enabled
		if confirmation.CredentialApplied {
			resource.ManagedChannel.LastConfirmedCredentialID = resource.Snapshot.Channel.CredentialID
			resource.ManagedChannel.LastConfirmedCredentialVersion = resource.Snapshot.Channel.CredentialVersion
		}
	}
	return nil
}

func (s *fakeSiteStore) CompleteSiteOperation(context.Context, reconciliation.Bundle, time.Time) error {
	s.completed = true
	s.bundle.Operation.Status = reconciliation.OperationSucceeded
	return nil
}

type siteTestGateway struct {
	*fakeGateway
	billingHash   string
	unknownUpdate bool
	failTestID    int64
}

func (g *siteTestGateway) ReadBillingBasis(context.Context) (map[string]json.RawMessage, string, error) {
	return nil, g.billingHash, nil
}

func (g *siteTestGateway) UpdateChannel(ctx context.Context, id int64, desired routing.DesiredChannel, secret []byte) error {
	if err := g.fakeGateway.UpdateChannel(ctx, id, desired, secret); err != nil {
		return err
	}
	if g.unknownUpdate {
		g.unknownUpdate = false
		return reconciliation.NewFailure(reconciliation.FailureUncertain, "write_result_unknown", "write result is unknown", nil)
	}
	return nil
}

func (g *siteTestGateway) TestChannel(ctx context.Context, id int64, model string, secret []byte) error {
	if id == g.failTestID {
		return reconciliation.NewFailure(reconciliation.FailureConfiguration, "upstream_test_failed", "upstream test failed", nil)
	}
	return g.fakeGateway.TestChannel(ctx, id, model, secret)
}

type siteTestFactory struct{ gateway reconciliation.Gateway }

func (factory siteTestFactory) New(string, []byte) (reconciliation.Gateway, error) {
	return factory.gateway, nil
}

type managedSiteGateway struct {
	*siteTestGateway
	batchCalls     int
	receivedSecret bool
}

func (g *managedSiteGateway) ReadManagedSyncCapabilities(context.Context) (reconciliation.ManagedSyncCapabilities, error) {
	return reconciliation.ManagedSyncCapabilities{
		ContractVersion: reconciliation.ManagedSyncContractVersion,
		NewAPIVersion:   "managed-test",
		DatabaseType:    "postgres",
		Features: reconciliation.ManagedSyncFeatures{
			AtomicApply: true, ManagedChannels: true, MultipleGroups: true, GroupRatios: true,
			EntryVisibility: true, PersistentIdempotency: true, FinalStateDigest: true,
		},
		Limits: reconciliation.ManagedSyncLimits{
			MaxChannels: 100, MaxGroups: 20, MaxModels: 500, MaxGroupKeyBytes: 64, MaxRequestBytes: 2 << 20,
		},
		BillingBasis:     map[string]json.RawMessage{"ModelRatio": json.RawMessage(`{}`)},
		BillingBasisHash: g.billingHash,
	}, nil
}

func (g *managedSiteGateway) ReadManagedState(context.Context) (reconciliation.ManagedSyncState, error) {
	return reconciliation.ManagedSyncState{
		StateHash: strings.Repeat("a", 64), BillingBasisHash: g.billingHash,
		Actual: reconciliation.ActualState{
			Version: "managed-test", GroupRatios: map[string]string{}, UserUsableGroups: map[string]string{},
		},
	}, nil
}

func (g *managedSiteGateway) ApplyManagedState(_ context.Context, request reconciliation.ManagedSyncRequest) (reconciliation.ManagedSyncResult, error) {
	g.batchCalls++
	actual := reconciliation.ActualState{
		Version: "managed-test", GroupRatios: make(map[string]string), UserUsableGroups: make(map[string]string),
		Channels: make([]reconciliation.ActualChannel, 0, len(request.Channels)),
	}
	for _, group := range request.Groups {
		actual.GroupRatios[group.Key] = group.SaleRatio
		if group.Visible {
			actual.UserUsableGroups[group.Key] = group.DisplayName
		}
	}
	for index, input := range request.Channels {
		g.receivedSecret = g.receivedSecret || string(input.APIKey) == "supplier-secret"
		status := reconciliation.ChannelEnabled
		if input.Desired.DesiredStatus == routing.DesiredDisabled {
			status = reconciliation.ChannelManuallyDisabled
		}
		channel := actualFromDesired(int64(81+index), input.Desired, status)
		channel.CredentialVersion = input.Desired.CredentialVersion
		actual.Channels = append(actual.Channels, channel)
	}
	return reconciliation.ManagedSyncResult{
		Actions: []reconciliation.ManagedSyncAction{{Resource: "channel", Key: request.Channels[0].Desired.ManagedTag, Action: "created", ChannelID: 81}},
		State: reconciliation.ManagedSyncState{
			StateHash: strings.Repeat("b", 64), BillingBasisHash: g.billingHash, Actual: actual,
		},
	}, nil
}

func siteFixture(t *testing.T) (*fakeSiteStore, *siteTestGateway, *reconciliation.Service) {
	t.Helper()
	bundle := testBundle()
	resource := reconciliation.ResourceBundle{
		Snapshot: bundle.Plan.Snapshot, ManagedChannel: bundle.ManagedChannel,
		SupplierCredential: bundle.SupplierCredential, CredentialAvailable: true,
	}
	bundle.Resources = []reconciliation.ResourceBundle{resource}
	bundle.CurrentPlanID = bundle.Plan.ID
	bundle.Plan.Snapshot = routing.Snapshot{
		SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: bundle.Site.ID,
		RelationID: resource.Snapshot.RelationID, SupplierID: resource.Snapshot.SupplierID,
		Resources: []routing.Snapshot{resource.Snapshot}, BillingBasisHash: strings.Repeat("1", 64),
	}
	store := &fakeSiteStore{fakeReconciliationStore: &fakeReconciliationStore{bundle: bundle}}
	gateway := &siteTestGateway{
		fakeGateway: &fakeGateway{state: reconciliation.ActualState{
			Version: "test", GroupRatios: map[string]string{"default": "1"}, UserUsableGroups: map[string]string{"default": "Default"},
		}}, billingHash: bundle.Plan.Snapshot.BillingBasisHash,
	}
	service, err := reconciliation.NewService(store, fakeVault{}, siteTestFactory{gateway}, nil, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	return store, gateway, service
}

func TestSiteRunUsesManagedBatchContractWhenAvailable(t *testing.T) {
	t.Parallel()
	store, legacy, _ := siteFixture(t)
	managed := &managedSiteGateway{siteTestGateway: legacy}
	service, err := reconciliation.NewService(store, fakeVault{}, siteTestFactory{managed}, nil, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || !store.pricesConfirmed || len(store.confirmations) != 1 {
		t.Fatalf("managed batch was not confirmed: completed=%v prices=%v confirmations=%d failure=%v", store.completed, store.pricesConfirmed, len(store.confirmations), store.failure)
	}
	if managed.batchCalls != 1 || !managed.receivedSecret {
		t.Fatalf("managed batch did not receive the expected request: calls=%d secret=%v", managed.batchCalls, managed.receivedSecret)
	}
	if legacy.createCount != 0 || legacy.updateCount != 0 || legacy.ratioCount != 0 || legacy.userGroupCount != 0 {
		t.Fatalf("legacy writes ran during managed batch: create=%d update=%d ratio=%d groups=%d", legacy.createCount, legacy.updateCount, legacy.ratioCount, legacy.userGroupCount)
	}
}

func existingSiteResource(store *fakeSiteStore, gateway *siteTestGateway, status reconciliation.ChannelStatus) {
	resource := &store.bundle.Resources[0]
	id, version, enabled := int64(42), int64(1), true
	resource.ManagedChannel.ExternalChannelID = &id
	resource.ManagedChannel.LastConfirmedPlanVersion = &version
	resource.ManagedChannel.LastConfirmedEnabled = &enabled
	resource.ManagedChannel.LastConfirmedCredentialID = resource.Snapshot.Channel.CredentialID
	resource.ManagedChannel.LastConfirmedCredentialVersion = resource.Snapshot.Channel.CredentialVersion
	gateway.state.Channels = []reconciliation.ActualChannel{actualFromDesired(id, resource.Snapshot.Channel, status)}
}

func TestSiteRunMultipleChannelsAndAutoPreservesOtherGroups(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	second := store.bundle.Resources[0]
	second.Snapshot.RelationID = uuid.MustParse("20000000-0000-0000-0000-000000000002")
	second.Snapshot.SupplierID = uuid.New()
	second.Snapshot.Channel.ID = uuid.New()
	second.Snapshot.Channel.ManagedTag = routing.ManagedTag(second.Snapshot.RelationID)
	second.Snapshot.Channel.GroupKey = routing.GroupKey(second.Snapshot.RelationID)
	second.Snapshot.Group.Key = second.Snapshot.Channel.GroupKey
	second.ManagedChannel.ID, second.ManagedChannel.RelationID, second.ManagedChannel.ManagedTag = second.Snapshot.Channel.ID, second.Snapshot.RelationID, second.Snapshot.Channel.ManagedTag
	store.bundle.Resources = append(store.bundle.Resources, second)
	auto := routing.DesiredGroup{Key: "mr_a_test_balanced", DisplayName: "Balanced", SaleRatio: "1.5", Visible: true}
	store.bundle.Plan.Snapshot.AutoGroups = []routing.DesiredGroup{auto}
	for index := range store.bundle.Resources {
		store.bundle.Resources[index].Snapshot.Channel.ExtraGroupKeys = []string{auto.Key}
	}
	store.bundle.Plan.Snapshot.Resources = []routing.Snapshot{store.bundle.Resources[0].Snapshot, store.bundle.Resources[1].Snapshot}
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || len(store.confirmations) != 2 || gateway.createCount != 2 {
		t.Fatalf("site did not confirm both channels: completed=%v resources=%d creates=%d failure=%v", store.completed, len(store.confirmations), gateway.createCount, store.failure)
	}
	if gateway.state.GroupRatios["default"] != "1" || gateway.state.GroupRatios[auto.Key] != "1.5" || gateway.state.UserUsableGroups["default"] != "Default" {
		t.Fatalf("site groups were not preserved: %#v", gateway.state)
	}
}

func TestSiteShutdownDoesNotNeedSupplierCredentialOrTest(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	existingSiteResource(store, gateway, reconciliation.ChannelEnabled)
	store.bundle.Resources[0].CredentialAvailable = false
	store.bundle.Resources[0].Snapshot.Channel.DesiredStatus = routing.DesiredDisabled
	store.bundle.Resources[0].Snapshot.Group.Visible = false
	store.bundle.Plan.Snapshot.Resources[0] = store.bundle.Resources[0].Snapshot
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || gateway.testCount != 0 || gateway.updateCount != 0 || gateway.state.Channels[0].Status != reconciliation.ChannelManuallyDisabled {
		t.Fatalf("shutdown depended on upstream or did not finish: completed=%v test=%d update=%d failure=%v", store.completed, gateway.testCount, gateway.updateCount, store.failure)
	}
}

func TestSiteCredentialOnlyChangeIsRewrittenAfterUnknownResult(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	existingSiteResource(store, gateway, reconciliation.ChannelEnabled)
	store.bundle.Resources[0].Snapshot.Channel.CredentialID = uuid.New()
	store.bundle.Resources[0].Snapshot.Channel.CredentialVersion = 2
	store.bundle.Plan.Snapshot.Resources[0] = store.bundle.Resources[0].Snapshot
	gateway.unknownUpdate = true
	err := service.Run(context.Background(), store.bundle.Operation.ID)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureUncertain || store.completed {
		t.Fatalf("unknown write was confirmed: %v", err)
	}
	if len(store.confirmations) != 0 {
		t.Fatal("unknown credential write was recorded as applied")
	}
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || gateway.updateCount != 2 || gateway.testCount != 1 || store.bundle.Resources[0].ManagedChannel.LastConfirmedCredentialVersion != 2 {
		t.Fatalf("credential change was skipped: completed=%v updates=%d tests=%d", store.completed, gateway.updateCount, gateway.testCount)
	}
}

func TestSiteManualLockNeedsExplicitResume(t *testing.T) {
	t.Parallel()
	for _, resume := range []bool{false, true} {
		store, gateway, service := siteFixture(t)
		existingSiteResource(store, gateway, reconciliation.ChannelManuallyDisabled)
		if resume {
			store.bundle.Plan.Snapshot.ResumeRelationIDs = []uuid.UUID{store.bundle.Resources[0].Snapshot.RelationID}
		}
		if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
			t.Fatal(err)
		}
		if store.completed != resume {
			t.Fatalf("resume=%v completed=%v failure=%v", resume, store.completed, store.failure)
		}
		if !resume && (gateway.enableCount != 0 || store.failure == nil || store.failure.Kind != reconciliation.FailureManualLock) {
			t.Fatal("manual safety lock was bypassed")
		}
		if !resume && store.failure.RelationID != store.bundle.Resources[0].Snapshot.RelationID {
			t.Fatal("manual safety lock was attributed to another supplier relation")
		}
	}
}

func TestSiteReloadUnderLockRejectsAnOlderPlan(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	store.replaceOnReload = uuid.New()
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.superseded || gateway.ratioCount != 0 || gateway.createCount != 0 || store.completed {
		t.Fatal("old plan performed external writes")
	}
}

func TestSiteBillingDriftBlocksActivation(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	gateway.billingHash = strings.Repeat("2", 64)
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if store.completed || gateway.createCount != 0 || store.failure == nil || store.failure.Code != "billing_basis_changed" {
		t.Fatal("billing drift did not block site writes")
	}
}

func TestSiteBillingDriftDoesNotBlockEmergencyShutdown(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	existingSiteResource(store, gateway, reconciliation.ChannelEnabled)
	store.bundle.Resources[0].CredentialAvailable = false
	store.bundle.Resources[0].Snapshot.Channel.DesiredStatus = routing.DesiredDisabled
	store.bundle.Plan.Snapshot.Resources[0] = store.bundle.Resources[0].Snapshot
	gateway.billingHash = strings.Repeat("2", 64)
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if gateway.state.Channels[0].Status != reconciliation.ChannelManuallyDisabled || len(store.confirmations) != 1 || gateway.testCount != 0 {
		t.Fatal("billing drift or unavailable upstream credentials prevented shutdown")
	}
	if store.completed || store.pricesConfirmed || gateway.ratioCount != 0 || store.failure == nil || store.failure.Code != "billing_basis_changed" {
		t.Fatal("billing drift was ignored after shutdown")
	}
}

func TestSitePartialFailureDoesNotConfirmTheSite(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	second := store.bundle.Resources[0]
	second.Snapshot.RelationID = uuid.MustParse("20000000-0000-0000-0000-000000000002")
	second.Snapshot.SupplierID = uuid.New()
	second.Snapshot.Channel.ID = uuid.New()
	second.Snapshot.Channel.ManagedTag = routing.ManagedTag(second.Snapshot.RelationID)
	second.Snapshot.Channel.GroupKey = routing.GroupKey(second.Snapshot.RelationID)
	second.Snapshot.Group.Key = second.Snapshot.Channel.GroupKey
	second.ManagedChannel.ID = second.Snapshot.Channel.ID
	store.bundle.Resources = append(store.bundle.Resources, second)
	store.bundle.Plan.Snapshot.Resources = append(store.bundle.Plan.Snapshot.Resources, second.Snapshot)
	gateway.failTestID = 43
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if store.completed || !store.pricesConfirmed || gateway.enableCount != 1 || len(store.confirmations) != 1 || store.failure == nil {
		t.Fatal("partial resource success was lost or the entire site was incorrectly confirmed")
	}
}

func TestSiteCredentialRotationPreservesShutdown(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	existingSiteResource(store, gateway, reconciliation.ChannelManuallyDisabled)
	store.bundle.Resources[0].Snapshot.Channel.DesiredStatus = routing.DesiredDisabled
	store.bundle.Resources[0].Snapshot.Channel.CredentialID = uuid.New()
	store.bundle.Resources[0].Snapshot.Channel.CredentialVersion = 2
	store.bundle.Plan.Snapshot.Resources[0] = store.bundle.Resources[0].Snapshot
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || gateway.updateCount != 1 || gateway.testCount != 1 || gateway.enableCount != 0 || gateway.state.Channels[0].Status != reconciliation.ChannelManuallyDisabled {
		t.Fatalf("rotation did not preserve shutdown: completed=%v updates=%d tests=%d enable=%d failure=%v", store.completed, gateway.updateCount, gateway.testCount, gateway.enableCount, store.failure)
	}
}

func TestSiteRevokedCredentialCannotBeConfirmedByUnchangedChannelFields(t *testing.T) {
	t.Parallel()
	store, gateway, service := siteFixture(t)
	existingSiteResource(store, gateway, reconciliation.ChannelEnabled)
	store.bundle.Resources[0].CredentialAvailable = false
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if store.completed || len(store.confirmations) != 0 || store.failure == nil || store.failure.Kind != reconciliation.FailureAuthentication {
		t.Fatal("revoked credential was accepted because visible channel configuration was unchanged")
	}
}
