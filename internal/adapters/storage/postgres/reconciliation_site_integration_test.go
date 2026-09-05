//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func openSiteIntegrationStore(t *testing.T) (context.Context, *postgres.Store) {
	t.Helper()
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return ctx, store
}

func TestCredentialPromotionWaitsForAllSitesIncludingDisabledChannels(t *testing.T) {
	ctx, store := openSiteIntegrationStore(t)
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x43}, platformcrypto.MasterKeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	onboard, err := onboarding.NewService(store, vault, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	supplierData, err := onboard.CreateSupplier(ctx, onboarding.CreateSupplierCommand{
		Code: "rotation-" + suffix, Name: "Rotation Supplier", UpstreamBaseURL: "https://upstream.example", UpstreamAPIKey: "old-test-secret",
		Models: []supplier.ModelInput{{Name: "model-a", UpstreamName: "model-a", InputPrice: decimal.Zero, OutputPrice: decimal.Zero, Currency: "USD"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]reconciliation.Bundle, 0, 2)
	for index := range 2 {
		siteData, err := onboard.CreateSite(ctx, onboarding.CreateSiteCommand{
			Code: fmt.Sprintf("rotation-%s-%d", suffix, index), Name: "Rotation Site", NewAPIBaseURL: "https://gateway.example", NewAPIAccessToken: "management-test-secret",
		})
		if err != nil {
			t.Fatal(err)
		}
		relation, _, err := onboard.CreateRelation(ctx, onboarding.CreateRelationCommand{SiteID: siteData.ID, SupplierID: supplierData.ID, GroupDisplayName: "Supplier", SaleRatio: decimal.NewFromInt(1), Visible: true})
		if err != nil {
			t.Fatal(err)
		}
		operation, err := store.CreateOperation(ctx, relation.ID, uuid.New(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := store.LoadBundle(ctx, operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BindChannel(ctx, bundle.ManagedChannel.ID, int64(index+101), time.Now()); err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, bundle)
	}
	queries := sqlc.New(store.Pool())
	candidate, err := vault.Encrypt(uuid.New(), credential.PurposeSupplierAPIKey, []byte("new-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateCredential(ctx, sqlc.CreateCredentialParams{
		ID: candidate.ID, Purpose: string(candidate.Purpose), Ciphertext: candidate.Ciphertext, Nonce: candidate.Nonce,
		KeyVersion: candidate.KeyVersion, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, "UPDATE suppliers SET pending_credential_id=$2, pending_credential_version=2 WHERE id=$1", supplierData.ID, candidate.ID); err != nil {
		t.Fatal(err)
	}
	for index := range bundles {
		resource := &bundles[index].Resources[0]
		resource.Snapshot.Channel.CredentialID = candidate.ID
		resource.Snapshot.Channel.CredentialVersion = 2
		if index == 1 {
			resource.Snapshot.Channel.DesiredStatus = routing.DesiredDisabled
		}
		externalID := int64(index + 101)
		if err := store.ConfirmResource(ctx, bundles[index], reconciliation.ResourceConfirmation{Resource: *resource, ExternalChannelID: &externalID, CredentialApplied: true}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteSiteOperation(ctx, bundles[index], time.Now()); err != nil {
			t.Fatal(err)
		}
		current, err := queries.GetSupplier(ctx, supplierData.ID)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 && (current.CredentialID != supplierData.CredentialID || !current.PendingCredentialID.Valid) {
			t.Fatal("credential was promoted before the other site confirmed")
		}
		if index == 1 && (current.CredentialID != candidate.ID || current.CredentialVersion != 2 || current.PendingCredentialID.Valid) {
			t.Fatal("credential was not promoted after both sites confirmed")
		}
	}
}

func TestSiteExecutionLockAlsoExcludesPlanPublication(t *testing.T) {
	ctx, store := openSiteIntegrationStore(t)
	siteID := uuid.New()
	lock, acquired, err := store.AcquireSiteLock(ctx, siteID)
	if err != nil || !acquired {
		t.Fatalf("acquire first site lock: acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lock.Release(ctx) }()
	if _, acquired, err := store.AcquireSiteLock(ctx, siteID); err != nil || acquired {
		t.Fatalf("same site lock was concurrently acquired: %v", err)
	}
	other, acquired, err := store.AcquireSiteLock(ctx, uuid.New())
	if err != nil || !acquired {
		t.Fatalf("different site was blocked: %v", err)
	}
	if err := other.Release(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := store.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var publicationAllowed bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1::text, 1))", siteID.String()).Scan(&publicationAllowed); err != nil {
		t.Fatal(err)
	}
	if publicationAllowed {
		t.Fatal("new plan could be published while synchronization was writing")
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1::text, 1))", siteID.String()).Scan(&publicationAllowed); err != nil {
		t.Fatal(err)
	}
	if !publicationAllowed {
		t.Fatal("site publication remained blocked after synchronization finished")
	}
}
