//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestM1OperationsAcrossSites(t *testing.T) {
	store, ctx := newOperationsTestStore(t)
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x42}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sealed := func(purpose credential.Purpose) *credential.Record {
		v, err := vault.Encrypt(uuid.New(), purpose, []byte("integration-test-credential"))
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	execute := func(m domain.Mutation) json.RawMessage {
		t.Helper()
		m.Actor = "integration-owner"
		if m.Key == "" {
			m.Key = uuid.NewString()
		}
		digest := sha256.Sum256([]byte(m.Key))
		m.RequestHash = hex.EncodeToString(digest[:])
		raw, err := store.MutateOperations(ctx, m)
		if err != nil {
			t.Fatalf("%s: %v", m.Kind, err)
		}
		return raw
	}
	getID := func(raw json.RawMessage) uuid.UUID {
		var v struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	siteIDs := []uuid.UUID{}
	for _, code := range []string{"east", "west"} {
		siteIDs = append(siteIDs, getID(execute(domain.Mutation{Kind: "create_site", Sealed: sealed(credential.PurposeNewAPIAdmin), Input: domain.SiteInput{Code: code, Name: code, NewAPIBaseURL: "https://" + code + ".example", AdminUserID: 1}})))
	}
	basis := domain.BillingBasis{Hash: strings.Repeat("a", 64), Values: map[string]json.RawMessage{"ModelRatio": json.RawMessage(`{"model-a":1}`)}}
	supplierIDs := make([]uuid.UUID, 0, 3)
	for _, code := range []string{"supplier-one", "supplier-two", "supplier-three"} {
		supplierID := getID(execute(domain.Mutation{Kind: "create_supplier", Sealed: sealed(credential.PurposeSupplierAPIKey), Input: domain.SupplierInput{Code: code, Name: code, BaseURL: "https://upstream.example", Models: []domain.ModelInput{{Model: "model-a", UpstreamModel: "model-a", InputPrice: "1.00", OutputPrice: "2.00", Currency: "USD", Enabled: true}}}}))
		supplierIDs = append(supplierIDs, supplierID)
		targets := []domain.DeploymentTarget{{SiteID: siteIDs[0], DisplayName: code, SaleRatio: "1.25", Visible: true}, {SiteID: siteIDs[1], DisplayName: code, SaleRatio: "1.50", Visible: false}}
		response := execute(domain.Mutation{Kind: "deploy", Input: domain.DeploymentInput{SupplierID: supplierID, Sites: targets, Reason: "two-site deployment"}, Bases: map[uuid.UUID]domain.BillingBasis{siteIDs[0]: basis, siteIDs[1]: basis}})
		if strings.Contains(string(response), `"credential_id"`) {
			t.Fatal("deployment response exposed a credential reference")
		}
	}
	for _, siteID := range siteIDs {
		var payload []byte
		var version int64
		if err := store.pool.QueryRow(ctx, `SELECT snapshot,version FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, siteID).Scan(&payload, &version); err != nil {
			t.Fatal(err)
		}
		snapshot, err := routing.DecodeSnapshot(payload)
		if err != nil {
			t.Fatal(err)
		}
		if version != 3 || len(snapshot.Resources) != 3 || snapshot.SiteID != siteID {
			t.Fatalf("whole-site plan: version=%d resources=%d", version, len(snapshot.Resources))
		}
	}
	execute(domain.Mutation{Kind: "update_supplier", ID: supplierIDs[0], Input: domain.SupplierInput{
		Name: "supplier-one", BaseURL: "https://upstream.example", Status: "enabled", Version: 1, Reason: "purchase price correction",
		Models: []domain.ModelInput{{Model: "model-a", UpstreamModel: "model-a", InputPrice: "1.10", OutputPrice: "2.20", Currency: "USD", Enabled: true}},
	}})
	for _, siteID := range siteIDs {
		var version int64
		if err := store.pool.QueryRow(ctx, `SELECT max(version) FROM route_plan_versions WHERE site_id=$1`, siteID).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 3 {
			t.Fatal("purchase price edit created an unchanged route plan")
		}
	}
	page, err := store.ListOperations(ctx, "relations", domain.Filter{SiteID: siteIDs[0], Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 {
		t.Fatalf("site filtering: %d", page.Total)
	}
	var relation struct {
		ID      uuid.UUID `json:"id"`
		Version int64     `json:"version"`
	}
	if err = json.Unmarshal(page.Items[0], &relation); err != nil {
		t.Fatal(err)
	}
	edit := domain.Mutation{Kind: "relation", ID: relation.ID, Actor: "integration-owner", Key: uuid.NewString(), Input: domain.RelationInput{Version: relation.Version, DisplayName: "renamed", Visible: false, DesiredStatus: "enabled", Reason: "site-only edit"}}
	first := execute(edit)
	second := execute(edit)
	var firstJSON, secondJSON any
	if err = json.Unmarshal(first, &firstJSON); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(second, &secondJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstJSON, secondJSON) {
		t.Fatal("idempotency replay changed")
	}
	var untouchedVersion int64
	if err = store.pool.QueryRow(ctx, `SELECT max(version) FROM route_plan_versions WHERE site_id=$1`, siteIDs[1]).Scan(&untouchedVersion); err != nil {
		t.Fatal(err)
	}
	if untouchedVersion != 3 {
		t.Fatal("editing one site generated a plan for another site")
	}
	edit.Key = uuid.NewString()
	edit.RequestHash = edit.Key
	if _, err = store.MutateOperations(ctx, edit); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	execute(domain.Mutation{Kind: "strategy", ID: siteIDs[0], StrategyKind: "balanced", Input: domain.StrategyInput{DisplayName: "Balanced", MemberRelationIDs: []uuid.UUID{relation.ID}, Reason: "configure auto"}})
	priceID := getID(execute(domain.Mutation{Kind: "draft_price", Input: domain.PriceInput{SiteID: siteIDs[0], GroupKey: domain.AutoGroupKey(siteIDs[0], "balanced"), SaleRatio: "1.2", Reason: "stable price"}, Bases: map[uuid.UUID]domain.BillingBasis{siteIDs[0]: basis}}))
	execute(domain.Mutation{Kind: "publish_price", ID: priceID, Input: domain.PublishInput{Version: 1}, Bases: map[uuid.UUID]domain.BillingBasis{siteIDs[0]: basis}})
	if _, err = store.pool.Exec(ctx, `UPDATE site_suppliers SET sync_status='active' WHERE site_id=$1`, siteIDs[0]); err != nil {
		t.Fatal(err)
	}
	execute(domain.Mutation{Kind: "strategy", ID: siteIDs[0], StrategyKind: "balanced", Input: domain.StrategyInput{Version: 1, Enabled: true, Visible: true, DisplayName: "Balanced", MemberRelationIDs: []uuid.UUID{relation.ID}, Reason: "manual admission"}})
	var payload []byte
	if err = store.pool.QueryRow(ctx, `SELECT snapshot FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, siteIDs[0]).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	snapshot, err := routing.DecodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.AutoGroups) != 1 || !snapshot.AutoGroups[0].Visible {
		t.Fatal("manual Auto group was not published")
	}
	var priceCount int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM price_versions WHERE site_id=$1 AND group_key=$2`, siteIDs[0], domain.AutoGroupKey(siteIDs[0], "balanced")).Scan(&priceCount); err != nil {
		t.Fatal(err)
	}
	if priceCount != 1 {
		t.Fatal("membership changed the price version")
	}
	var jobCount int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='reconcile_route_plan_v1'`).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount < 6 {
		t.Fatalf("persisted jobs missing: %d", jobCount)
	}
}

func TestM1RejectsDuplicateNewAPIBaseURL(t *testing.T) {
	store, ctx := newOperationsTestStore(t)
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x24}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	create := func(code string) error {
		sealed, err := vault.Encrypt(uuid.New(), credential.PurposeNewAPIAdmin, []byte("integration-test-credential"))
		if err != nil {
			return err
		}
		key := uuid.NewString()
		_, err = store.MutateOperations(ctx, domain.Mutation{
			Kind: "create_site", Actor: "integration-owner", Key: key, RequestHash: key, Sealed: &sealed,
			Input: domain.SiteInput{Code: code, Name: code, NewAPIBaseURL: "https://same-new-api.example", AdminUserID: 1},
		})
		return err
	}
	if err := create("first-site"); err != nil {
		t.Fatal(err)
	}
	err = create("second-site")
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23505" || databaseError.ConstraintName != "sites_new_api_base_url_unique" {
		t.Fatalf("duplicate New API site was not rejected by its ownership constraint: %v", err)
	}
}

func newOperationsTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := "operations_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close(context.Background())
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	if err = Migrate(ctx, parsed.String()); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store, ctx
}
