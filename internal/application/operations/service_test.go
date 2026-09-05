package operations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
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
