//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
)

type operationsReviewFixture struct {
	store         *Store
	ctx           context.Context
	siteID        uuid.UUID
	supplierID    uuid.UUID
	supplierInput domain.SupplierInput
	vault         *platformcrypto.Vault
}

func (fixture operationsReviewFixture) execute(m domain.Mutation) (json.RawMessage, error) {
	m.Actor = "review-integration"
	m.Key = uuid.NewString()
	encoded, err := json.Marshal(m.Input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	m.RequestHash = hex.EncodeToString(hash[:])
	return fixture.store.MutateOperations(fixture.ctx, m)
}

func newOperationsReviewFixture(t *testing.T) operationsReviewFixture {
	t.Helper()
	store, ctx := newOperationsTestStore(t)
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x45}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	fixture := operationsReviewFixture{store: store, ctx: ctx, vault: vault}
	create := func(m domain.Mutation) uuid.UUID {
		t.Helper()
		raw, err := fixture.execute(m)
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		return record.ID
	}
	admin, err := vault.Encrypt(uuid.New(), credential.PurposeNewAPIAdmin, []byte("review-management-token"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.siteID = create(domain.Mutation{Kind: "create_site", Sealed: &admin, Input: domain.SiteInput{Code: "review-site", Name: "Review Site", NewAPIBaseURL: "https://gateway.example", AdminUserID: 1}})
	supplierKey, err := vault.Encrypt(uuid.New(), credential.PurposeSupplierAPIKey, []byte("review-upstream-token"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.supplierInput = domain.SupplierInput{Code: "review-supplier", Name: "Review Supplier", BaseURL: "https://upstream.example", Models: []domain.ModelInput{{Model: "model-a", UpstreamModel: "model-a", InputPrice: "1", OutputPrice: "2", Currency: "USD", Enabled: true}}}
	fixture.supplierID = create(domain.Mutation{Kind: "create_supplier", Sealed: &supplierKey, Input: fixture.supplierInput})
	_, err = fixture.execute(domain.Mutation{Kind: "deploy", Input: domain.DeploymentInput{SupplierID: fixture.supplierID, Sites: []domain.DeploymentTarget{{SiteID: fixture.siteID, DisplayName: "Supplier", SaleRatio: "1", Visible: true}}, Reason: "review deployment"}, Bases: map[uuid.UUID]domain.BillingBasis{fixture.siteID: {Hash: strings.Repeat("a", 64), Values: map[string]json.RawMessage{"ModelRatio": json.RawMessage(`{"model-a":1}`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestM1PendingCredentialStillAllowsEmergencySupplierShutdown(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	candidate, err := fixture.vault.Encrypt(uuid.New(), credential.PurposeSupplierAPIKey, []byte("review-candidate-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "rotate_credential", ID: fixture.supplierID, Sealed: &candidate, Input: domain.CredentialInput{Version: 1, Reason: "candidate pending site confirmation"}}); err != nil {
		t.Fatal(err)
	}
	input := fixture.supplierInput
	input.Version, input.Status, input.Reason = 2, "disabled", "emergency shutdown while candidate failed"
	if _, err := fixture.execute(domain.Mutation{Kind: "update_supplier", ID: fixture.supplierID, Input: input}); err != nil {
		t.Fatalf("pending credential blocked emergency shutdown: %v", err)
	}
	var payload []byte
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT snapshot FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, fixture.siteID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	snapshot, err := routing.DecodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Resources[0].Channel.DesiredStatus != routing.DesiredDisabled {
		t.Fatal("shutdown did not generate a disabled target")
	}
}

func TestM1RestoreRejectsHistoricalModelsThatAreNoLongerAvailable(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	var oldPlanID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT id FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, fixture.siteID).Scan(&oldPlanID); err != nil {
		t.Fatal(err)
	}
	input := fixture.supplierInput
	input.Version, input.Status, input.Reason = 1, "enabled", "supplier changed its model catalog"
	input.Models = []domain.ModelInput{{Model: "model-b", UpstreamModel: "model-b", InputPrice: "1", OutputPrice: "2", Currency: "USD", Enabled: true}}
	if _, err := fixture.execute(domain.Mutation{Kind: "update_supplier", ID: fixture.supplierID, Input: input}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "restore", ID: oldPlanID, Input: domain.RestoreInput{Reason: "restore model-a historical plan"}}); err == nil {
		t.Fatal("restore silently substituted current model-b for historical model-a")
	}
}

func TestM1RestorePreservesDisabledStrategyMemberSelection(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	var relationID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT id FROM site_suppliers WHERE site_id=$1`, fixture.siteID).Scan(&relationID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "strategy", ID: fixture.siteID, StrategyKind: "balanced", Input: domain.StrategyInput{
		DisplayName: "Saved disabled strategy", Enabled: false, MemberRelationIDs: []uuid.UUID{relationID}, Reason: "save selected member while disabled",
	}}); err != nil {
		t.Fatal(err)
	}
	var historicalPlanID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT id FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, fixture.siteID).Scan(&historicalPlanID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "strategy", ID: fixture.siteID, StrategyKind: "balanced", Input: domain.StrategyInput{
		Version: 1, DisplayName: "Changed strategy", Enabled: false, MemberRelationIDs: []uuid.UUID{}, Reason: "remove selected member",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "restore", ID: historicalPlanID, Input: domain.RestoreInput{Reason: "restore saved disabled strategy"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	var enabled bool
	var name string
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT display_name,enabled,(SELECT count(*) FROM strategy_members WHERE strategy_id=s.id) FROM site_strategies s WHERE site_id=$1 AND kind='balanced'`, fixture.siteID).Scan(&name, &enabled, &count); err != nil {
		t.Fatal(err)
	}
	if enabled || count != 1 || name != "Saved disabled strategy" {
		t.Fatalf("disabled strategy source selection was lost: enabled=%v members=%d name=%q", enabled, count, name)
	}
}

func TestM1CandidateReplacementAndCancellationPublishFreshPlans(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	var originalCredentialID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT credential_id FROM suppliers WHERE id=$1`, fixture.supplierID).Scan(&originalCredentialID); err != nil {
		t.Fatal(err)
	}
	var lastCandidateID uuid.UUID
	candidateIDs := make([]uuid.UUID, 0, 2)
	for version := int64(1); version <= 2; version++ {
		candidate, err := fixture.vault.Encrypt(uuid.New(), credential.PurposeSupplierAPIKey, []byte("review-replacement-token"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.execute(domain.Mutation{Kind: "rotate_credential", ID: fixture.supplierID, Sealed: &candidate, Input: domain.CredentialInput{Version: version, Reason: "replace pending candidate"}}); err != nil {
			t.Fatal(err)
		}
		lastCandidateID = candidate.ID
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	var firstCandidateRevoked bool
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT revoked_at IS NOT NULL FROM credentials WHERE id=$1`, candidateIDs[0]).Scan(&firstCandidateRevoked); err != nil {
		t.Fatal(err)
	}
	if !firstCandidateRevoked {
		t.Fatal("replaced credential candidate remained usable in local credential storage")
	}
	var replacementPlanID uuid.UUID
	var payload []byte
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT id,snapshot FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, fixture.siteID).Scan(&replacementPlanID, &payload); err != nil {
		t.Fatal(err)
	}
	snapshot, err := routing.DecodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Resources[0].Channel.CredentialID != lastCandidateID || snapshot.Resources[0].Channel.CredentialVersion != 3 {
		t.Fatal("replacement candidate was not published with a newer credential version")
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "cancel_credential", ID: fixture.supplierID, Input: domain.CredentialCancelInput{Version: 3, Reason: "restore the verified current credential"}}); err != nil {
		t.Fatal(err)
	}
	var rollbackPlanID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT id,snapshot FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, fixture.siteID).Scan(&rollbackPlanID, &payload); err != nil {
		t.Fatal(err)
	}
	snapshot, err = routing.DecodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if rollbackPlanID == replacementPlanID || snapshot.Resources[0].Channel.CredentialID != originalCredentialID || snapshot.Resources[0].Channel.CredentialVersion != 1 {
		t.Fatal("cancellation failed to publish a fresh rollback plan using the current credential")
	}
	var cancelledCandidateRevoked bool
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT revoked_at IS NOT NULL FROM credentials WHERE id=$1`, candidateIDs[1]).Scan(&cancelledCandidateRevoked); err != nil {
		t.Fatal(err)
	}
	if !cancelledCandidateRevoked {
		t.Fatal("cancelled credential candidate remained usable in local credential storage")
	}
}

func TestM1ReplacingSiteManagementCredentialRevokesTheStoredPredecessor(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	var oldCredentialID uuid.UUID
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT admin_credential_id FROM sites WHERE id=$1`, fixture.siteID).Scan(&oldCredentialID); err != nil {
		t.Fatal(err)
	}
	replacement, err := fixture.vault.Encrypt(uuid.New(), credential.PurposeNewAPIAdmin, []byte("replacement-management-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.execute(domain.Mutation{Kind: "update_site", ID: fixture.siteID, Sealed: &replacement, Input: domain.SiteInput{
		Name: "Review Site", NewAPIBaseURL: "https://gateway.example", AdminUserID: 1, Status: "enabled", Version: 1, Reason: "replace management credential",
	}}); err != nil {
		t.Fatal(err)
	}
	var currentCredentialID uuid.UUID
	var predecessorRevoked bool
	if err = fixture.store.pool.QueryRow(fixture.ctx, `SELECT admin_credential_id FROM sites WHERE id=$1`, fixture.siteID).Scan(&currentCredentialID); err != nil {
		t.Fatal(err)
	}
	if err = fixture.store.pool.QueryRow(fixture.ctx, `SELECT revoked_at IS NOT NULL FROM credentials WHERE id=$1`, oldCredentialID).Scan(&predecessorRevoked); err != nil {
		t.Fatal(err)
	}
	if currentCredentialID != replacement.ID || !predecessorRevoked {
		t.Fatal("site management credential replacement did not revoke its stored predecessor")
	}
}
