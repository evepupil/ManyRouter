package reconciliation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

type fakeReconciliationStore struct {
	mutex     sync.Mutex
	bundle    reconciliation.Bundle
	steps     []reconciliation.StepRecord
	failure   *reconciliation.FailureRecord
	completed bool
	boundID   *int64
	lock      *fakeSiteLock
}

func (s *fakeReconciliationStore) CreateOperation(context.Context, uuid.UUID, uuid.UUID, time.Time) (reconciliation.Operation, error) {
	return s.bundle.Operation, nil
}

func (s *fakeReconciliationStore) GetOperation(context.Context, uuid.UUID) (reconciliation.Operation, error) {
	return s.bundle.Operation, nil
}

func (s *fakeReconciliationStore) LoadBundle(context.Context, uuid.UUID) (reconciliation.Bundle, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.bundle, nil
}

func (s *fakeReconciliationStore) AcquireSiteLock(context.Context, uuid.UUID) (reconciliation.SiteLock, bool, error) {
	s.lock = &fakeSiteLock{}
	return s.lock, true, nil
}

func (s *fakeReconciliationStore) StartOperation(_ context.Context, operation reconciliation.Operation, _ string, _ time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.bundle.Operation = operation
	s.bundle.Operation.Status = reconciliation.OperationRunning
	return nil
}

func (s *fakeReconciliationStore) RecordStep(_ context.Context, record reconciliation.StepRecord) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.steps = append(s.steps, record)
	return nil
}

func (s *fakeReconciliationStore) BindChannel(_ context.Context, _ uuid.UUID, externalID int64, _ time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.boundID = &externalID
	s.bundle.ManagedChannel.ExternalChannelID = &externalID
	return nil
}

func (s *fakeReconciliationStore) CompleteOperation(_ context.Context, _ reconciliation.Bundle, externalID int64, _ time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.completed = true
	s.bundle.Operation.Status = reconciliation.OperationSucceeded
	s.bundle.ManagedChannel.ExternalChannelID = &externalID
	return nil
}

func (s *fakeReconciliationStore) FailOperation(_ context.Context, record reconciliation.FailureRecord) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.failure = &record
	s.bundle.Operation.Status = reconciliation.OperationManualRequired
	switch record.Kind {
	case reconciliation.FailureRetryable:
		s.bundle.Operation.Status = reconciliation.OperationRetryableFailed
	case reconciliation.FailureUncertain:
		s.bundle.Operation.Status = reconciliation.OperationUncertain
	}
	return nil
}

type fakeSiteLock struct {
	released bool
}

func (l *fakeSiteLock) Release(context.Context) error {
	l.released = true
	return nil
}

type fakeVault struct{}

func (fakeVault) Decrypt(record credential.Record) ([]byte, error) {
	if record.Purpose == credential.PurposeNewAPIAdmin {
		return []byte("admin-secret"), nil
	}
	return []byte("supplier-secret"), nil
}

type fakeGatewayFactory struct {
	gateway *fakeGateway
}

func (f fakeGatewayFactory) New(string, []byte) (reconciliation.Gateway, error) {
	return f.gateway, nil
}

type fakeGateway struct {
	mutex          sync.Mutex
	state          reconciliation.ActualState
	createCount    int
	updateCount    int
	testCount      int
	enableCount    int
	ratioCount     int
	userGroupCount int
	ratioFailure   error
	desiredChannel routing.DesiredChannel
}

func (g *fakeGateway) Probe(context.Context) (string, error) {
	return g.state.Version, nil
}

func (g *fakeGateway) ReadActualState(context.Context) (reconciliation.ActualState, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return cloneActualState(g.state), nil
}

func (g *fakeGateway) SetGroupRatios(_ context.Context, ratios map[string]string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.ratioFailure != nil {
		return g.ratioFailure
	}
	g.ratioCount++
	g.state.GroupRatios = cloneMap(ratios)
	return nil
}

func (g *fakeGateway) SetUserUsableGroups(_ context.Context, groups map[string]string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.userGroupCount++
	g.state.UserUsableGroups = cloneMap(groups)
	return nil
}

func (g *fakeGateway) CreateChannel(_ context.Context, desired routing.DesiredChannel, _ []byte) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.createCount++
	g.desiredChannel = desired
	g.state.Channels = append(g.state.Channels, actualFromDesired(int64(41+g.createCount), desired, reconciliation.ChannelManuallyDisabled))
	return nil
}

func (g *fakeGateway) UpdateChannel(_ context.Context, id int64, desired routing.DesiredChannel, _ []byte) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.updateCount++
	for index := range g.state.Channels {
		if g.state.Channels[index].ID == id {
			status := g.state.Channels[index].Status
			g.state.Channels[index] = actualFromDesired(id, desired, status)
		}
	}
	return nil
}

func (g *fakeGateway) TestChannel(context.Context, int64, string, []byte) error {
	g.testCount++
	return nil
}

func (g *fakeGateway) SetChannelEnabled(_ context.Context, id int64, enabled bool) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.enableCount++
	for index := range g.state.Channels {
		if g.state.Channels[index].ID == id {
			g.state.Channels[index].Status = reconciliation.ChannelManuallyDisabled
			if enabled {
				g.state.Channels[index].Status = reconciliation.ChannelEnabled
			}
		}
	}
	return nil
}

func TestRunCreatesTestsEnablesAndConfirmsChannel(t *testing.T) {
	t.Parallel()
	bundle := testBundle()
	store := &fakeReconciliationStore{bundle: bundle}
	gateway := &fakeGateway{state: reconciliation.ActualState{
		Version:          "0ed497f0",
		GroupRatios:      map[string]string{"default": "1"},
		UserUsableGroups: map[string]string{"default": "Default"},
	}}
	service := newReconciliationService(t, store, gateway)

	if err := service.Run(context.Background(), bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if !store.completed || store.boundID == nil || *store.boundID != 42 {
		t.Fatalf("operation was not confirmed: completed=%v bound=%v", store.completed, store.boundID)
	}
	if gateway.createCount != 1 || gateway.updateCount != 1 || gateway.testCount != 1 || gateway.enableCount != 1 || gateway.ratioCount != 1 || gateway.userGroupCount != 1 {
		t.Fatalf("unexpected gateway calls: create=%d update=%d test=%d enable=%d ratio=%d user_groups=%d", gateway.createCount, gateway.updateCount, gateway.testCount, gateway.enableCount, gateway.ratioCount, gateway.userGroupCount)
	}
	if gateway.state.GroupRatios["default"] != "1" || gateway.state.GroupRatios[bundle.Plan.Snapshot.Group.Key] != "1.25" {
		t.Fatalf("group ratios were not preserved: %#v", gateway.state.GroupRatios)
	}
	if !store.lock.released {
		t.Fatal("site lock was not released")
	}

	if err := service.Run(context.Background(), bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if gateway.createCount != 1 {
		t.Fatalf("duplicate job created another channel: %d", gateway.createCount)
	}
}

func TestRunPreservesManualDisableAfterConfirmedPlan(t *testing.T) {
	t.Parallel()
	bundle := testBundle()
	externalID := int64(42)
	confirmedVersion := int64(1)
	bundle.ManagedChannel.ExternalChannelID = &externalID
	bundle.ManagedChannel.LastConfirmedPlanVersion = &confirmedVersion
	store := &fakeReconciliationStore{bundle: bundle}
	gateway := &fakeGateway{state: reconciliation.ActualState{
		Version:          "0ed497f0",
		GroupRatios:      map[string]string{"default": "1", bundle.Plan.Snapshot.Group.Key: "1.25"},
		UserUsableGroups: map[string]string{"default": "Default", bundle.Plan.Snapshot.Group.Key: bundle.Plan.Snapshot.Group.DisplayName},
		Channels: []reconciliation.ActualChannel{
			actualFromDesired(externalID, bundle.Plan.Snapshot.Channel, reconciliation.ChannelManuallyDisabled),
		},
	}}
	service := newReconciliationService(t, store, gateway)

	if err := service.Run(context.Background(), bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if store.failure == nil || store.failure.Kind != reconciliation.FailureManualLock {
		t.Fatalf("manual lock was not recorded: %#v", store.failure)
	}
	if gateway.enableCount != 0 || gateway.testCount != 0 {
		t.Fatalf("manual disable was overridden: enable=%d test=%d", gateway.enableCount, gateway.testCount)
	}
}

func TestRunRecordsUnknownWriteWithoutCreatingChannel(t *testing.T) {
	t.Parallel()
	bundle := testBundle()
	store := &fakeReconciliationStore{bundle: bundle}
	gateway := &fakeGateway{
		state: reconciliation.ActualState{
			Version: "0ed497f0", GroupRatios: map[string]string{"default": "1"}, UserUsableGroups: map[string]string{"default": "Default"},
		},
		ratioFailure: reconciliation.NewFailure(reconciliation.FailureUncertain, "write_result_unknown", "write result is unknown", nil),
	}
	service := newReconciliationService(t, store, gateway)

	err := service.Run(context.Background(), bundle.Operation.ID)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureUncertain {
		t.Fatalf("expected uncertain failure, got %v", err)
	}
	if store.failure == nil || store.failure.Kind != reconciliation.FailureUncertain || store.failure.NextAttemptAt == nil {
		t.Fatalf("unknown result was not persisted: %#v", store.failure)
	}
	if gateway.createCount != 0 {
		t.Fatalf("channel was created after unknown ratio write: %d", gateway.createCount)
	}
}

func newReconciliationService(t *testing.T, store *fakeReconciliationStore, gateway *fakeGateway) *reconciliation.Service {
	t.Helper()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	service, err := reconciliation.NewService(store, fakeVault{}, fakeGatewayFactory{gateway: gateway}, nil, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testBundle() reconciliation.Bundle {
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	relationID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	supplierID := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	planID := uuid.MustParse("40000000-0000-0000-0000-000000000001")
	operationID := uuid.MustParse("50000000-0000-0000-0000-000000000001")
	channelID := uuid.MustParse("60000000-0000-0000-0000-000000000001")
	adminCredentialID := uuid.MustParse("70000000-0000-0000-0000-000000000001")
	supplierCredentialID := uuid.MustParse("80000000-0000-0000-0000-000000000001")
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	snapshot := routing.Snapshot{
		SchemaVersion: routing.SnapshotSchemaVersion,
		SiteID:        siteID,
		RelationID:    relationID,
		SupplierID:    supplierID,
		Group: routing.DesiredGroup{
			Key:         routing.GroupKey(relationID),
			DisplayName: "Supplier A",
			SaleRatio:   "1.25",
			Visible:     true,
		},
		Channel: routing.DesiredChannel{
			ID:                channelID,
			ManagedTag:        routing.ManagedTag(relationID),
			Name:              "Supplier A [ManyRouter]",
			Protocol:          "openai_compatible",
			BaseURL:           "https://upstream.example/v1",
			CredentialID:      supplierCredentialID,
			CredentialVersion: 1,
			Models:            []routing.ModelRoute{{Model: "model-a", UpstreamModel: "upstream-a"}},
			GroupKey:          routing.GroupKey(relationID),
			Priority:          0,
			Weight:            100,
			DesiredStatus:     routing.DesiredEnabled,
		},
	}
	return reconciliation.Bundle{
		Operation: reconciliation.Operation{
			ID: operationID, SiteID: siteID, RelationID: relationID, RoutePlanID: planID,
			Status: reconciliation.OperationPending, CreatedAt: now, UpdatedAt: now,
		},
		Site: site.Site{
			ID: siteID, NewAPIBaseURL: "https://gateway.example", AdminCredentialID: adminCredentialID,
			Status: site.StatusEnabled, CompatibilityStatus: site.CompatibilityUnknown,
		},
		Plan:               routing.Plan{ID: planID, SiteID: siteID, RelationID: relationID, Version: 1, Snapshot: snapshot},
		ManagedChannel:     routing.ManagedChannel{ID: channelID, RelationID: relationID, ManagedTag: snapshot.Channel.ManagedTag},
		AdminCredential:    credential.Record{ID: adminCredentialID, Purpose: credential.PurposeNewAPIAdmin, KeyVersion: 1},
		SupplierCredential: credential.Record{ID: supplierCredentialID, Purpose: credential.PurposeSupplierAPIKey, KeyVersion: 1},
	}
}

func actualFromDesired(id int64, desired routing.DesiredChannel, status reconciliation.ChannelStatus) reconciliation.ActualChannel {
	models := make([]string, 0, len(desired.Models))
	mapping := make(map[string]string)
	for _, model := range desired.Models {
		models = append(models, model.Model)
		if model.Model != model.UpstreamModel {
			mapping[model.Model] = model.UpstreamModel
		}
	}
	return reconciliation.ActualChannel{
		ID: id, ManagedTag: desired.ManagedTag, Name: desired.Name, Protocol: desired.Protocol,
		BaseURL: desired.BaseURL, Models: models, ModelMapping: mapping, Groups: desired.GroupKeys(),
		Priority: desired.Priority, Weight: desired.Weight, Status: status,
	}
}

func cloneActualState(input reconciliation.ActualState) reconciliation.ActualState {
	result := input
	result.GroupRatios = cloneMap(input.GroupRatios)
	result.UserUsableGroups = cloneMap(input.UserUsableGroups)
	result.Channels = append([]reconciliation.ActualChannel(nil), input.Channels...)
	return result
}

func cloneMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
