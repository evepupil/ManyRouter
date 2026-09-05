package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

type replayOnlyStore struct {
	Store
	result json.RawMessage
	err    error
}

func (s replayOnlyStore) GetOperationReplay(context.Context, domain.Mutation) (json.RawMessage, bool, error) {
	return s.result, s.result != nil || s.err != nil, s.err
}

func TestCompletedCredentialRotationCanBeRetriedAfterVersionChanged(t *testing.T) {
	// Replaying an accepted request must not contact an upstream or revalidate its obsolete input version.
	expected := json.RawMessage(`{"plans":[{"id":"accepted-operation"}]}`)
	service := &Service{store: replayOnlyStore{result: expected}}
	result, err := service.Execute(context.Background(), domain.Mutation{Kind: "rotate_credential", Actor: "owner", Key: "retry-key-123", Input: domain.CredentialInput{Version: 1, APIKey: "previously-validated-key", Reason: "rotation"}})
	if err != nil || string(result) != string(expected) {
		t.Fatalf("accepted rotation was not replayed: %v", err)
	}
}

func TestReusedRequestIdentityRejectsChangedContent(t *testing.T) {
	service := &Service{store: replayOnlyStore{err: domain.ErrConflict}}
	_, err := service.Execute(context.Background(), domain.Mutation{Kind: "sync", Actor: "owner", Key: "retry-key-123"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reused request identity accepted: %v", err)
	}
}

type preflightStore struct{ Store }

func (preflightStore) GetOperationReplay(context.Context, domain.Mutation) (json.RawMessage, bool, error) {
	return nil, false, nil
}
func (preflightStore) GetSiteAccess(context.Context, uuid.UUID) (domain.SiteAccess, error) {
	return domain.SiteAccess{BaseURL: "https://site.example", AdminUserID: 1}, nil
}

type rejectedGateway struct{ reconciliation.Gateway }

func (rejectedGateway) Probe(context.Context) (string, error) { return "test", nil }
func (rejectedGateway) ReadActualState(context.Context) (reconciliation.ActualState, error) {
	return reconciliation.ActualState{}, errors.New("insufficient permissions")
}

type rejectedFactory struct{}

func (rejectedFactory) NewForSite(string, []byte, int64) (reconciliation.Gateway, error) {
	return rejectedGateway{}, nil
}

func TestManagementCredentialIsCheckedBeforeReplacingIt(t *testing.T) {
	service := &Service{store: preflightStore{}, gateways: rejectedFactory{}}
	_, err := service.Execute(context.Background(), domain.Mutation{Kind: "update_site", ID: uuid.New(), Actor: "owner", Key: "change-management-key", Input: domain.SiteInput{Version: 1, Name: "Site", NewAPIBaseURL: "https://site.example", AdminUserID: 1, Status: "enabled", AccessToken: "new-management-key", Reason: "rotation"}})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid management credential was accepted: %v", err)
	}
}

func TestSiteAddressChangeNeverSendsTheCurrentCredentialToTheNewAddress(t *testing.T) {
	service := &Service{store: preflightStore{}, gateways: rejectedFactory{}}
	_, err := service.Execute(context.Background(), domain.Mutation{Kind: "update_site", ID: uuid.New(), Actor: "owner", Key: "change-site-address", Input: domain.SiteInput{Version: 1, Name: "Site", NewAPIBaseURL: "https://other.example", AdminUserID: 1, Status: "enabled", Reason: "migration"}})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("site address change reused the current management credential: %v", err)
	}
}

type strategyPreflightStore struct {
	Store
	mutations int
}

func (s *strategyPreflightStore) GetOperationReplay(context.Context, domain.Mutation) (json.RawMessage, bool, error) {
	return nil, false, nil
}

func (s *strategyPreflightStore) GetSiteAccess(context.Context, uuid.UUID) (domain.SiteAccess, error) {
	return domain.SiteAccess{BaseURL: "https://site.example", AdminUserID: 1}, nil
}

func (s *strategyPreflightStore) MutateOperations(context.Context, domain.Mutation) (json.RawMessage, error) {
	s.mutations++
	return json.RawMessage(`{"id":"accepted"}`), nil
}

type strategyVault struct{ Vault }

func (strategyVault) Decrypt(credential.Record) ([]byte, error) {
	return []byte("management-secret"), nil
}

type strategyGateway struct {
	reconciliation.Gateway
	state reconciliation.ActualState
}

func (gateway strategyGateway) ReadActualState(context.Context) (reconciliation.ActualState, error) {
	return gateway.state, nil
}

type strategyGatewayFactory struct {
	state reconciliation.ActualState
}

func (factory strategyGatewayFactory) NewForSite(string, []byte, int64) (reconciliation.Gateway, error) {
	return strategyGateway{state: factory.state}, nil
}

func TestNewAutoStrategyRejectsAnOccupiedFixedGroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state reconciliation.ActualState
		want  bool
	}{
		{name: "ratio entry", state: reconciliation.ActualState{GroupRatios: map[string]string{"mrab": "1"}}, want: true},
		{name: "user entry", state: reconciliation.ActualState{UserUsableGroups: map[string]string{"mrab": "Balanced"}}, want: true},
		{name: "channel membership", state: reconciliation.ActualState{Channels: []reconciliation.ActualChannel{{Groups: []string{"default", "mrab"}}}}, want: true},
		{name: "available", state: reconciliation.ActualState{GroupRatios: map[string]string{"default": "1"}, UserUsableGroups: map[string]string{"default": "Default"}, Channels: []reconciliation.ActualChannel{{Groups: []string{"default"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &strategyPreflightStore{}
			service := &Service{store: store, vault: strategyVault{}, gateways: strategyGatewayFactory{state: test.state}}
			_, err := service.Execute(context.Background(), domain.Mutation{
				Kind: "strategy", ID: uuid.New(), StrategyKind: "balanced", Actor: "owner", Key: "create-auto-strategy",
				Input: domain.StrategyInput{DisplayName: "Balanced", Reason: "configure Auto"},
			})
			if test.want {
				if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "固定 Auto 分组短码已被站点现有配置占用") {
					t.Fatalf("occupied fixed group was accepted: %v", err)
				}
				if store.mutations != 0 {
					t.Fatal("occupied fixed group reached persistence")
				}
				return
			}
			if err != nil {
				t.Fatalf("available fixed group was rejected: %v", err)
			}
			if store.mutations != 1 {
				t.Fatalf("available fixed group mutation count: %d", store.mutations)
			}
		})
	}
}

func TestExistingAutoStrategySkipsFixedGroupOwnershipPreflight(t *testing.T) {
	t.Parallel()
	store := &strategyPreflightStore{}
	service := &Service{store: store}
	_, err := service.Execute(context.Background(), domain.Mutation{
		Kind: "strategy", ID: uuid.New(), StrategyKind: "balanced", Actor: "owner", Key: "update-auto-strategy",
		Input: domain.StrategyInput{Version: 1, DisplayName: "Balanced", Reason: "update Auto"},
	})
	if err != nil {
		t.Fatalf("existing strategy unexpectedly ran ownership preflight: %v", err)
	}
	if store.mutations != 1 {
		t.Fatalf("existing strategy mutation count: %d", store.mutations)
	}
}
