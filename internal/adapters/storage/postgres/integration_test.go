//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestMigrateAndPersistM0Onboarding(t *testing.T) {
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var riverTable *string
	if err := store.Pool().QueryRow(ctx, "SELECT to_regclass('public.river_job')::text").Scan(&riverTable); err != nil {
		t.Fatal(err)
	}
	if riverTable == nil || *riverTable != "river_job" {
		t.Fatalf("River migrations were not applied: %v", riverTable)
	}

	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x31}, platformcrypto.MasterKeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	service, err := onboarding.NewService(store, vault, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	siteData, err := service.CreateSite(ctx, onboarding.CreateSiteCommand{
		Code: "site-" + suffix, Name: "Integration Site", NewAPIBaseURL: "http://127.0.0.1:3000", NewAPIAccessToken: "database-admin-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	supplierData, err := service.CreateSupplier(ctx, onboarding.CreateSupplierCommand{
		Code: "supplier-" + suffix, Name: "Integration Supplier", UpstreamBaseURL: "https://upstream.example/v1", UpstreamAPIKey: "database-supplier-secret",
		Models: []supplier.ModelInput{{
			Name: "model-a", UpstreamName: "model-a", InputPrice: decimal.RequireFromString("0.0000012345"),
			OutputPrice: decimal.RequireFromString("0.0000067890"), Currency: "USD",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	relation, plan, err := service.CreateRelation(ctx, onboarding.CreateRelationCommand{
		SiteID: siteData.ID, SupplierID: supplierData.ID, GroupDisplayName: "Integration Supplier",
		SaleRatio: decimal.RequireFromString("1.250000"), Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	storedRelation, err := service.GetRelation(ctx, relation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRelation.CurrentPlanID != plan.ID || storedRelation.CurrentPlanVersion != 1 || !storedRelation.SaleRatio.Equal(decimal.RequireFromString("1.25")) {
		t.Fatalf("stored relation does not match route plan: relation=%#v plan=%#v", storedRelation, plan)
	}
	storedSupplier, err := service.GetSupplier(ctx, supplierData.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedSupplier.Models) != 1 || !storedSupplier.Models[0].InputPrice.Equal(decimal.RequireFromString("0.0000012345")) {
		t.Fatalf("supplier price lost precision: %#v", storedSupplier.Models)
	}

	var credentialCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT count(*)
		FROM credentials
		WHERE id IN ($1, $2)
		  AND ciphertext <> $3::bytea
		  AND ciphertext <> $4::bytea
	`, siteData.AdminCredentialID, supplierData.CredentialID, []byte("database-admin-secret"), []byte("database-supplier-secret")).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 2 {
		t.Fatalf("credentials were not stored as authenticated ciphertext: count=%d", credentialCount)
	}
}
