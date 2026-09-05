//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestM1ContendedConfigurationLockDoesNotExhaustThePool(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	tx, err := fixture.store.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(fixture.ctx)) }()
	if _, err := tx.Exec(fixture.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('manyrouter_operator_configuration',2))`); err != nil {
		t.Fatal(err)
	}
	assertConcurrentOperationsAreBusy(t, fixture, 10)
	assertReviewPoolAvailable(t, fixture)
}

func TestM1SiteSynchronizationLockReturnsBusyWithoutWaiting(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	lock, acquired, err := fixture.store.AcquireSiteLock(fixture.ctx, fixture.siteID)
	if err != nil || !acquired {
		t.Fatalf("acquire worker site lock: acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lock.Release(context.WithoutCancel(fixture.ctx)) }()
	assertConcurrentOperationsAreBusy(t, fixture, 10)
	assertReviewPoolAvailable(t, fixture)
}

func assertConcurrentOperationsAreBusy(t *testing.T, fixture operationsReviewFixture, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(fixture.ctx, 2*time.Second)
	defer cancel()
	results := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := fixture.store.MutateOperations(ctx, domain.Mutation{
				Kind: "sync", ID: fixture.siteID, Actor: "lock-review", Key: uuid.NewString(), RequestHash: "controlled-lock-review",
			})
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, domain.ErrBusy) {
			t.Fatalf("contended operation did not return busy promptly: %v", err)
		}
	}
}

func assertReviewPoolAvailable(t *testing.T, fixture operationsReviewFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(fixture.ctx, time.Second)
	defer cancel()
	var value int
	if err := fixture.store.pool.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatalf("worker could not use the pool while configuration lock was held: %v", err)
	}
}

type preparedLegacyDeployment struct {
	relation routing.Relation
	channel  routing.ManagedChannel
	snapshot routing.Snapshot
	payload  []byte
	hash     string
}

func prepareLegacyDeployment(t *testing.T, fixture operationsReviewFixture) preparedLegacyDeployment {
	t.Helper()
	sealed, err := fixture.vault.Encrypt(uuid.New(), credential.PurposeNewAPIAdmin, []byte("legacy-review-admin-token"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fixture.execute(domain.Mutation{Kind: "create_site", Sealed: &sealed, Input: domain.SiteInput{Code: "legacy-site", Name: "Legacy Site", NewAPIBaseURL: "https://legacy.example", AdminUserID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	site, err := fixture.store.GetSite(fixture.ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	supplier, err := fixture.store.GetSupplier(fixture.ctx, fixture.supplierID)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := routing.NewRelation(uuid.New(), site.ID, supplier.ID, "Legacy Supplier", decimal.NewFromInt(1), true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := routing.NewManagedChannel(uuid.New(), relation.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := routing.BuildSnapshot(site, supplier, relation, channel)
	if err != nil {
		t.Fatal(err)
	}
	payload, hash, err := routing.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return preparedLegacyDeployment{relation: relation, channel: channel, snapshot: snapshot, payload: payload, hash: hash}
}

func TestM0PreparedDeploymentRechecksM1StateUnderLock(t *testing.T) {
	for _, changed := range []string{"supplier_disabled", "site_paused", "pending_credential"} {
		t.Run(changed, func(t *testing.T) {
			fixture := newOperationsReviewFixture(t)
			prepared := prepareLegacyDeployment(t, fixture)
			switch changed {
			case "supplier_disabled":
				input := fixture.supplierInput
				input.Version, input.Status, input.Reason = 1, "disabled", "disable during legacy preparation"
				if _, err := fixture.execute(domain.Mutation{Kind: "update_supplier", ID: fixture.supplierID, Input: input}); err != nil {
					t.Fatal(err)
				}
			case "site_paused":
				if _, err := fixture.execute(domain.Mutation{Kind: "update_site", ID: prepared.relation.SiteID, Input: domain.SiteInput{Name: "Legacy Site", NewAPIBaseURL: "https://legacy.example", AdminUserID: 1, Version: 1, Status: "disabled", Reason: "pause during legacy preparation"}}); err != nil {
					t.Fatal(err)
				}
			case "pending_credential":
				candidate, err := fixture.vault.Encrypt(uuid.New(), credential.PurposeSupplierAPIKey, []byte("legacy-review-candidate-token"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.execute(domain.Mutation{Kind: "rotate_credential", ID: fixture.supplierID, Sealed: &candidate, Input: domain.CredentialInput{Version: 1, Reason: "rotate during legacy preparation"}}); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := fixture.store.CreateRelationAndPlan(fixture.ctx, prepared.relation, prepared.channel, uuid.New(), prepared.snapshot, prepared.payload, prepared.hash, "legacy prepared before update", "review-owner")
			if !errors.Is(err, onboarding.ErrInvalidInput) {
				t.Fatalf("stale legacy deployment was not rejected after %s: %v", changed, err)
			}
			var count int
			if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM site_suppliers WHERE site_id=$1`, prepared.relation.SiteID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("rejected legacy deployment left a relation behind")
			}
		})
	}
}

func TestM0PreparedDeploymentRejectsModelCatalogChangedBeforeLock(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	prepared := prepareLegacyDeployment(t, fixture)
	input := fixture.supplierInput
	input.Version, input.Status, input.Reason = 1, "enabled", "change catalog during legacy preparation"
	input.Models = []domain.ModelInput{{Model: "model-b", UpstreamModel: "model-b", InputPrice: "1", OutputPrice: "2", Currency: "USD", Enabled: true}}
	if _, err := fixture.execute(domain.Mutation{Kind: "update_supplier", ID: fixture.supplierID, Input: input}); err != nil {
		t.Fatal(err)
	}
	_, _, err := fixture.store.CreateRelationAndPlan(fixture.ctx, prepared.relation, prepared.channel, uuid.New(), prepared.snapshot, prepared.payload, prepared.hash, "legacy prepared before model update", "review-owner")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("legacy deployment did not reject a changed model catalog: %v", err)
	}
	var count int
	if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM site_suppliers WHERE site_id=$1`, prepared.relation.SiteID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected legacy deployment left a relation behind")
	}
}

func TestM1ConfirmedPriceRemainsAppliedWhenLaterChannelSynchronizationFails(t *testing.T) {
	fixture := newOperationsReviewFixture(t)
	loadLatest := func() reconciliation.Bundle {
		t.Helper()
		var operationID uuid.UUID
		if err := fixture.store.pool.QueryRow(fixture.ctx, `SELECT o.id FROM sync_operations o JOIN route_plan_versions p ON p.id=o.route_plan_id WHERE o.site_id=$1 ORDER BY p.version DESC LIMIT 1`, fixture.siteID).Scan(&operationID); err != nil {
			t.Fatal(err)
		}
		bundle, err := fixture.store.LoadBundle(fixture.ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	original := loadLatest()
	if len(original.Plan.Snapshot.PriceVersionIDs) != 1 {
		t.Fatal("fixture requires one dedicated group price")
	}
	originalPriceID := original.Plan.Snapshot.PriceVersionIDs[0]
	if err := fixture.store.ConfirmSitePrices(fixture.ctx, original, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	basis := domain.BillingBasis{Hash: strings.Repeat("a", 64), Values: map[string]json.RawMessage{"ModelRatio": json.RawMessage(`{"model-a":1}`)}}
	raw, err := fixture.execute(domain.Mutation{Kind: "draft_price", Input: domain.PriceInput{SiteID: fixture.siteID, GroupKey: original.Plan.Snapshot.Resources[0].Group.Key, SaleRatio: "2", Reason: "publish reviewed new price"}, Bases: map[uuid.UUID]domain.BillingBasis{fixture.siteID: basis}})
	if err != nil {
		t.Fatal(err)
	}
	var price struct {
		ID      uuid.UUID `json:"id"`
		Version int64     `json:"version"`
	}
	if err := json.Unmarshal(raw, &price); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.execute(domain.Mutation{Kind: "publish_price", ID: price.ID, Input: domain.PublishInput{Version: price.Version}, Bases: map[uuid.UUID]domain.BillingBasis{fixture.siteID: basis}}); err != nil {
		t.Fatal(err)
	}
	latest := loadLatest()
	if err := fixture.store.StartOperation(fixture.ctx, latest.Operation, "read_actual", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmSitePrices(fixture.ctx, latest, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.FailOperation(fixture.ctx, reconciliation.FailureRecord{OperationID: latest.Operation.ID, SiteID: fixture.siteID, RelationID: latest.Operation.RelationID, RoutePlanID: latest.Plan.ID, Kind: reconciliation.FailureManualLock, Step: "channel:apply", Code: "channel_manually_disabled", Message: "channel was manually stopped after price confirmation", OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		id      uuid.UUID
		applied bool
	}{{originalPriceID, false}, {price.ID, true}} {
		raw, err := fixture.store.GetOperationResource(fixture.ctx, "prices", expected.id)
		if err != nil {
			t.Fatal(err)
		}
		var actual struct {
			LastConfirmed *bool  `json:"is_last_confirmed"`
			Status        string `json:"status"`
		}
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		if actual.LastConfirmed == nil || *actual.LastConfirmed != expected.applied || (expected.applied && actual.Status != "applied") {
			t.Fatalf("price confirmation was replaced by the whole-site failure: want last-confirmed=%v status=%s", expected.applied, actual.Status)
		}
	}
	operation, err := fixture.store.GetOperation(fixture.ctx, latest.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != reconciliation.OperationManualRequired {
		t.Fatal("site operation did not retain its channel failure")
	}
}
