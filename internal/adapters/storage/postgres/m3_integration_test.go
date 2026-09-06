//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	catalogapp "github.com/evepupil/ManyRouter/internal/application/catalog"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type readyAutomationCompatibility struct{}

func (readyAutomationCompatibility) CheckAutomationCompatibility(context.Context, uuid.UUID) (automationapp.Compatibility, error) {
	return automationapp.Compatibility{Ready: true}, nil
}

func TestM3AutomaticDecisionCreatesOnePlanAndCatalogStaysSiteScoped(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	fixture := createM2IntegrationFixture(t, ctx, store)
	now := time.Now().UTC().Truncate(time.Microsecond)
	strategyID, dedicatedPriceID, autoPriceID := uuid.New(), uuid.New(), uuid.New()
	var initialPlanID uuid.UUID
	var resourceJSON []byte
	if err := store.Pool().QueryRow(ctx, `
		SELECT id,snapshot FROM route_plan_versions
		WHERE site_id=$1 ORDER BY version DESC LIMIT 1
	`, fixture.siteID).Scan(&initialPlanID, &resourceJSON); err != nil {
		t.Fatal(err)
	}
	resource, err := routing.DecodeSnapshot(resourceJSON)
	if err != nil {
		t.Fatal(err)
	}
	resource.Channel.DesiredStatus = routing.DesiredEnabled
	resource.Group.Visible = true
	resource.Group.DisplayName = "公开专属线路"
	if _, err := store.Pool().Exec(ctx, `
		UPDATE site_suppliers
		SET desired_status='enabled',sync_status='active',current_plan_id=$2,
		    group_display_name='公开专属线路',updated_at=$3
		WHERE id=$1
	`, fixture.relationID, initialPlanID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO site_strategies(id,site_id,kind,group_key,display_name,enabled,visible,version,created_at,updated_at)
		VALUES($1,$2,'balanced','mrab','均衡',true,true,1,$3,$3)
	`, strategyID, fixture.siteID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO strategy_versions(strategy_id,version,snapshot,reason,actor_id,created_at)
		VALUES($1,1,'{}','integration setup','test',$2)
	`, strategyID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO strategy_automation_settings(strategy_id,mode,version,entry_closed_by_automation,reason,updated_by,updated_at)
		VALUES($1,'automatic',1,false,'integration setup','test',$2)
	`, strategyID, now); err != nil {
		t.Fatal(err)
	}
	basisHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO price_versions(id,site_id,group_key,version,sale_ratio,reason,status,billing_basis,basis_hash,created_at,published_at,applied_at,route_plan_id)
		VALUES
			($1,$3,$4,1,1,'integration setup','applied','{}',$7,$6,$6,$6,$5),
			($2,$3,'mrab',1,1.2,'integration setup','applied','{}',$7,$6,$6,$6,$5)
	`, dedicatedPriceID, autoPriceID, fixture.siteID, resource.Group.Key, initialPlanID, now, basisHash); err != nil {
		t.Fatal(err)
	}
	siteSnapshot := routing.Snapshot{
		SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: fixture.siteID,
		RelationID: fixture.relationID, SupplierID: fixture.supplierID,
		Resources:        []routing.Snapshot{resource},
		AutoGroups:       []routing.DesiredGroup{{Key: "mrab", DisplayName: "均衡", SaleRatio: "1.2", Visible: true}},
		PriceVersionIDs:  []uuid.UUID{dedicatedPriceID, autoPriceID},
		StrategyVersions: []routing.StrategyReference{{ID: strategyID, Version: 1}},
		BillingBasisHash: basisHash,
	}
	sort.Slice(siteSnapshot.PriceVersionIDs, func(i, j int) bool {
		return siteSnapshot.PriceVersionIDs[i].String() < siteSnapshot.PriceVersionIDs[j].String()
	})
	payload, hash, err := routing.EncodeSnapshot(siteSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		UPDATE route_plan_versions
		SET snapshot=$2,content_hash=$3,status='confirmed',confirmed_at=$4
		WHERE id=$1
	`, initialPlanID, payload, hash, now); err != nil {
		t.Fatal(err)
	}
	scoreRunID, scoreSnapshotID := uuid.New(), uuid.New()
	windowEnd := now.Add(-time.Minute).Truncate(time.Minute)
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO score_runs(id,site_id,policy_version,window_end,expected_targets,completed_targets,status,started_at,completed_at)
		VALUES($1,$2,'m2-shadow-v1',$3,1,1,'succeeded',$4,$4)
	`, scoreRunID, fixture.siteID, windowEnd, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO score_snapshots(
			id,site_id,supplier_id,model,policy_version,window_start,window_end,
			passive_samples,active_samples,quality_score,total_score,confidence,eligibility,
			hard_reasons,explanation,score_run_id,created_at
		) VALUES($1,$2,$3,$4,'m2-shadow-v1',$5::timestamptz-INTERVAL '24 hours',$5,200,4,90,90,'high','eligible','[]','{}',$6,$7)
	`, scoreSnapshotID, fixture.siteID, fixture.supplierID, fixture.model, windowEnd, scoreRunID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO shadow_recommendations(
			id,score_snapshot_id,site_id,supplier_id,model,strategy_kind,action,current_member,score,confidence,reasons,created_at
		) VALUES($1,$2,$3,$4,$5,'balanced','join',false,90,'high','["join_threshold_met"]',$6)
	`, uuid.New(), scoreSnapshotID, fixture.siteID, fixture.supplierID, fixture.model, now); err != nil {
		t.Fatal(err)
	}
	automationService, err := automationapp.NewService(store, readyAutomationCompatibility{}, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	run, err := automationService.Process(ctx, scoreRunID, automationapp.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != automationapp.RunPendingSync || run.RoutePlanID == nil || len(run.Decisions) != 1 || run.Decisions[0].Action != "join" {
		t.Fatalf("unexpected automatic run: %#v", run)
	}
	var memberCount, planCount int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM strategy_members WHERE strategy_id=$1`, strategyID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM route_plan_versions WHERE site_id=$1`, fixture.siteID).Scan(&planCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 1 || planCount != 2 {
		t.Fatalf("automatic decision was not atomic: members=%d plans=%d", memberCount, planCount)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE route_plan_versions SET status='confirmed',confirmed_at=$2 WHERE id=$1`, *run.RoutePlanID, now); err != nil {
		t.Fatal(err)
	}
	catalogService, err := catalogapp.NewService(
		store, bytes.NewReader(bytes.Repeat([]byte{9}, 64)), func() time.Time { return now }, uuid.New,
	)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := catalogService.CreateToken(ctx, fixture.siteID, "integration user page", "integration-owner")
	if err != nil {
		t.Fatal(err)
	}
	products, err := catalogService.GetProducts(ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if products.SiteID != fixture.siteID || products.RoutePlanID != *run.RoutePlanID || len(products.Products) != 2 {
		t.Fatalf("unexpected site products: %#v", products)
	}
	encoded, err := json.Marshal(products)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("M2 Integration Supplier")) || bytes.Contains(encoded, []byte("m2-supplier-secret")) {
		t.Fatal("site products exposed internal supplier data")
	}
	if err := catalogService.RevokeToken(ctx, fixture.siteID, issued.ID, "integration cleanup", "integration-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogService.GetProducts(ctx, issued.Token); err == nil {
		t.Fatal("revoked site product token remained usable")
	}
	excludeAt := now.Add(5 * time.Minute)
	excludeRunID := insertM3Recommendation(t, ctx, store, fixture, excludeAt, "exclude", true, []string{"authenticity_mismatch"})
	excludeService, err := automationapp.NewService(store, readyAutomationCompatibility{}, func() time.Time { return excludeAt }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := excludeService.Process(ctx, excludeRunID, automationapp.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if excluded.RoutePlanID == nil {
		t.Fatal("hard risk did not create a route plan")
	}
	var held, visible bool
	if err := store.Pool().QueryRow(ctx, `
		SELECT hold.active,strategy.visible
		FROM site_supplier_automation_holds hold
		JOIN site_strategies strategy ON strategy.id=$2
		WHERE hold.relation_id=$1
	`, fixture.relationID, strategyID).Scan(&held, &visible); err != nil {
		t.Fatal(err)
	}
	if !held || visible {
		t.Fatalf("hard risk did not close the last-member Auto: held=%v visible=%v", held, visible)
	}
	assertM3PlanChannelStatus(t, ctx, store, *excluded.RoutePlanID, routing.DesiredDisabled)
	confirmM3Plan(t, ctx, store, fixture.relationID, *excluded.RoutePlanID, excludeAt)
	recoverAt := now.Add(10 * time.Minute)
	recoverRunID := insertM3Recommendation(t, ctx, store, fixture, recoverAt, "join", false, nil)
	recoverService, err := automationapp.NewService(store, readyAutomationCompatibility{}, func() time.Time { return recoverAt }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := recoverService.Process(ctx, recoverRunID, automationapp.TriggerScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RoutePlanID == nil || len(recovered.Decisions) != 1 || recovered.Decisions[0].Action != "recover" {
		t.Fatalf("unexpected recovery run: %#v", recovered)
	}
	if err := store.Pool().QueryRow(ctx, `
		SELECT hold.active,strategy.visible
		FROM site_supplier_automation_holds hold
		JOIN site_strategies strategy ON strategy.id=$2
		WHERE hold.relation_id=$1
	`, fixture.relationID, strategyID).Scan(&held, &visible); err != nil {
		t.Fatal(err)
	}
	if held || !visible {
		t.Fatalf("automatic hold was not recovered: held=%v visible=%v", held, visible)
	}
	assertM3PlanChannelStatus(t, ctx, store, *recovered.RoutePlanID, routing.DesiredEnabled)
}

func insertM3Recommendation(
	t *testing.T,
	ctx context.Context,
	store interface{ Pool() *pgxpool.Pool },
	fixture m2IntegrationFixture,
	now time.Time,
	action string,
	currentMember bool,
	hardReasons []string,
) uuid.UUID {
	t.Helper()
	runID, snapshotID := uuid.New(), uuid.New()
	windowEnd := now.Add(-time.Minute).Truncate(time.Minute)
	hardJSON, err := json.Marshal(hardReasons)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO score_runs(id,site_id,policy_version,window_end,expected_targets,completed_targets,status,started_at,completed_at)
		VALUES($1,$2,'m2-shadow-v1',$3,1,1,'succeeded',$4,$4)
	`, runID, fixture.siteID, windowEnd, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO score_snapshots(
			id,site_id,supplier_id,model,policy_version,window_start,window_end,
			passive_samples,active_samples,quality_score,total_score,confidence,eligibility,
			hard_reasons,explanation,score_run_id,created_at
		) VALUES($1,$2,$3,$4,'m2-shadow-v1',$5::timestamptz-INTERVAL '24 hours',$5,200,4,90,90,'high','eligible',$6,'{}',$7,$8)
	`, snapshotID, fixture.siteID, fixture.supplierID, fixture.model, windowEnd, hardJSON, runID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		INSERT INTO shadow_recommendations(
			id,score_snapshot_id,site_id,supplier_id,model,strategy_kind,action,current_member,score,confidence,reasons,created_at
		) VALUES($1,$2,$3,$4,$5,'balanced',$6,$7,90,'high','[]',$8)
	`, uuid.New(), snapshotID, fixture.siteID, fixture.supplierID, fixture.model, action, currentMember, now); err != nil {
		t.Fatal(err)
	}
	return runID
}

func confirmM3Plan(t *testing.T, ctx context.Context, store interface{ Pool() *pgxpool.Pool }, relationID, planID uuid.UUID, now time.Time) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `UPDATE route_plan_versions SET status='confirmed',confirmed_at=$2 WHERE id=$1`, planID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		UPDATE site_suppliers SET current_plan_id=$2,sync_status='active',updated_at=$3 WHERE id=$1
	`, relationID, planID, now); err != nil {
		t.Fatal(err)
	}
}

func assertM3PlanChannelStatus(t *testing.T, ctx context.Context, store interface{ Pool() *pgxpool.Pool }, planID uuid.UUID, want routing.DesiredStatus) {
	t.Helper()
	var payload []byte
	if err := store.Pool().QueryRow(ctx, `SELECT snapshot FROM route_plan_versions WHERE id=$1`, planID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	plan, err := routing.DecodeSnapshot(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Resources) != 1 || plan.Resources[0].Channel.DesiredStatus != want {
		t.Fatalf("unexpected channel status in plan: %#v", plan.Resources)
	}
}
