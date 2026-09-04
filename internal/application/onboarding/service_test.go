package onboarding_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeOnboardingStore struct {
	siteData     site.Site
	supplierData supplier.Supplier
	relation     routing.Relation
	plan         routing.Plan
	credentials  []credential.Record
}

func (s *fakeOnboardingStore) CreateSite(_ context.Context, data site.Site, sealed credential.Record, _ string) error {
	s.siteData = data
	s.credentials = append(s.credentials, sealed)
	return nil
}

func (s *fakeOnboardingStore) GetSite(context.Context, uuid.UUID) (site.Site, error) {
	return s.siteData, nil
}

func (s *fakeOnboardingStore) CreateSupplier(_ context.Context, data supplier.Supplier, sealed credential.Record, _ string) error {
	s.supplierData = data
	s.credentials = append(s.credentials, sealed)
	return nil
}

func (s *fakeOnboardingStore) GetSupplier(context.Context, uuid.UUID) (supplier.Supplier, error) {
	return s.supplierData, nil
}

func (s *fakeOnboardingStore) CreateRelationAndPlan(
	_ context.Context,
	relation routing.Relation,
	_ routing.ManagedChannel,
	planID uuid.UUID,
	snapshot routing.Snapshot,
	payload []byte,
	hash string,
	reason string,
	_ string,
) (routing.Relation, routing.Plan, error) {
	relation.CurrentPlanID = planID
	relation.CurrentPlanVersion = 1
	s.relation = relation
	s.plan = routing.Plan{
		ID: planID, SiteID: relation.SiteID, RelationID: relation.ID, Version: 1,
		Snapshot: snapshot, SnapshotJSON: append([]byte(nil), payload...), ContentHash: hash,
		Reason: reason, Status: routing.PlanPending, CreatedAt: relation.CreatedAt,
	}
	return relation, s.plan, nil
}

func (s *fakeOnboardingStore) GetRelation(context.Context, uuid.UUID) (routing.Relation, error) {
	return s.relation, nil
}

type fakeSealer struct {
	plaintexts [][]byte
}

func (s *fakeSealer) Encrypt(id uuid.UUID, purpose credential.Purpose, plaintext []byte) (credential.Record, error) {
	s.plaintexts = append(s.plaintexts, append([]byte(nil), plaintext...))
	return credential.Record{ID: id, Purpose: purpose, Ciphertext: []byte("sealed"), Nonce: []byte("nonce"), KeyVersion: 1}, nil
}

func TestCreateSiteSupplierAndRelationBuildsCredentialFreePlan(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		uuid.MustParse("10000000-0000-0000-0000-000000000002"),
		uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		uuid.MustParse("30000000-0000-0000-0000-000000000001"),
		uuid.MustParse("30000000-0000-0000-0000-000000000002"),
		uuid.MustParse("30000000-0000-0000-0000-000000000003"),
	}
	index := 0
	newID := func() uuid.UUID {
		id := ids[index]
		index++
		return id
	}
	store := &fakeOnboardingStore{}
	sealer := &fakeSealer{}
	service, err := onboarding.NewService(store, sealer, func() time.Time { return now }, newID)
	if err != nil {
		t.Fatal(err)
	}
	siteData, err := service.CreateSite(context.Background(), onboarding.CreateSiteCommand{
		Code: "site-a", Name: "Site A", NewAPIBaseURL: "https://gateway.example/", NewAPIAccessToken: "admin-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	supplierData, err := service.CreateSupplier(context.Background(), onboarding.CreateSupplierCommand{
		Code: "supplier-a", Name: "Supplier A", UpstreamBaseURL: "https://upstream.example/v1/", UpstreamAPIKey: "supplier-secret",
		Models: []supplier.ModelInput{
			{Name: "model-b", UpstreamName: "model-b", InputPrice: decimal.Zero, OutputPrice: decimal.Zero, Currency: "usd"},
			{Name: "model-a", UpstreamName: "upstream-a", InputPrice: decimal.RequireFromString("0.1"), OutputPrice: decimal.RequireFromString("0.2"), Currency: "USD"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	relation, plan, err := service.CreateRelation(context.Background(), onboarding.CreateRelationCommand{
		SiteID: siteData.ID, SupplierID: supplierData.ID, GroupDisplayName: "Supplier A", SaleRatio: decimal.RequireFromString("1.25"), Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if siteData.NewAPIBaseURL != "https://gateway.example" || supplierData.UpstreamBaseURL != "https://upstream.example" {
		t.Fatalf("base URLs were not normalized: site=%q supplier=%q", siteData.NewAPIBaseURL, supplierData.UpstreamBaseURL)
	}
	if supplierData.Models[0].Name != "model-a" || relation.GroupKey != routing.GroupKey(relation.ID) {
		t.Fatalf("business data was not normalized: models=%#v relation=%#v", supplierData.Models, relation)
	}
	if bytes.Contains(plan.SnapshotJSON, []byte("admin-secret")) || bytes.Contains(plan.SnapshotJSON, []byte("supplier-secret")) {
		t.Fatalf("route plan contains plaintext credentials: %s", plan.SnapshotJSON)
	}
	if len(store.credentials) != 2 || string(store.credentials[0].Ciphertext) != "sealed" || string(store.credentials[1].Ciphertext) != "sealed" {
		t.Fatalf("credentials were not sealed: %#v", store.credentials)
	}
	if len(sealer.plaintexts) != 2 || string(sealer.plaintexts[0]) != "admin-secret" || string(sealer.plaintexts[1]) != "supplier-secret" {
		t.Fatalf("unexpected values passed to the credential sealer")
	}
}
