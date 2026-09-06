//go:build acceptance && contract

package compatibility_test

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestM2RealAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal("acceptance workspace unavailable")
	}
	run := &acceptanceRun{
		t: t, ctx: ctx, root: root, client: &http.Client{Timeout: 90 * time.Second},
		ownedTags: make(map[string]bool),
	}
	run.evidence = acceptanceEvidence{
		Version: 2, Versions: make(map[string]string), Counts: make(map[string]int),
		Checks: make(map[string]bool), Stages: make([]acceptanceStage, 0),
	}
	run.evidencePath = filepath.Join(root, ".cache", "m2-acceptance-"+time.Now().UTC().Format("20060102150405.000000000")+".evidence.json")
	defer run.finish()

	run.must("configuration", run.loadConfiguration())
	run.must("acceptance_lock", run.acquireAcceptanceLock())
	run.must("ephemeral_database", run.prepareM2Database())
	run.must("independent_gateways", run.prepareM2Gateways())
	run.must("m2_backend", run.startM2Backend())
	run.must("suppliers_and_deployments", run.seedM2Deployments())
	workerContext, stopWorker := context.WithCancel(context.Background())
	run.workerCancel = stopWorker
	run.must("start_worker", run.worker.Start(workerContext))
	run.must("first_gateway_sync", run.waitSite(0, false))
	run.must("second_gateway_sync", run.waitSite(1, false))
	run.must("manual_strategies", run.configureStrategies())
	run.must("real_traffic", run.exerciseM2Traffic())
	run.must("log_collection", run.verifyM2Collection())
	run.must("health_and_quality", run.verifyM2BaselineEvaluations())
	run.must("authenticity", run.verifyM2Authenticity())
	run.must("shadow_scoring", run.verifyM2Scoring())
	run.evidence.Checks["main_chain_completed"] = true
	t.Logf("M2 acceptance evidence: %s", run.evidencePath)
}

func (run *acceptanceRun) prepareM2Database() error {
	key := make([]byte, 32)
	if _, err := cryptorand.Read(key); err != nil {
		return err
	}
	defer clear(key)
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	run.state = acceptanceState{
		Version: 1, Schema: "m2a_" + id, Prefix: "m2a-" + id[:12],
		MasterKey: base64.StdEncoding.EncodeToString(key),
	}
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	admin, err := pgx.Connect(run.ctx, databaseURL)
	if err != nil {
		return acceptanceFault{"database_unavailable"}
	}
	schema := pgx.Identifier{run.state.Schema}.Sanitize()
	if _, err := admin.Exec(run.ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(context.Background())
		return err
	}
	if err := admin.Close(run.ctx); err != nil {
		return err
	}
	run.t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		connection, connectErr := pgx.Connect(cleanup, databaseURL)
		if connectErr != nil {
			run.t.Error("M2 acceptance schema cleanup connection failed")
			return
		}
		defer func() { _ = connection.Close(cleanup) }()
		if _, dropErr := connection.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			run.t.Error("M2 acceptance schema cleanup failed")
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return acceptanceFault{"database_configuration_invalid"}
	}
	query := parsed.Query()
	query.Set("search_path", run.state.Schema)
	parsed.RawQuery = query.Encode()
	if err := postgres.Migrate(run.ctx, parsed.String()); err != nil {
		return err
	}
	run.store, err = postgres.Open(run.ctx, parsed.String())
	if err != nil {
		return err
	}
	run.t.Cleanup(run.store.Close)
	_, err = run.store.Pool().Exec(run.ctx, `
		CREATE TABLE acceptance_temp_keys (
			site_slot integer NOT NULL,
			token_name text NOT NULL,
			token_id bigint,
			deleted boolean NOT NULL DEFAULT false,
			PRIMARY KEY(site_slot, token_name)
		)
	`)
	return err
}

func (run *acceptanceRun) prepareM2Gateways() error {
	binary := os.Getenv("MANYROUTER_NEW_API_BINARY")
	run.sites = make([]acceptanceSite, 0, 2)
	for slot := 0; slot < 2; slot++ {
		baseURL := startNewAPI(run.t, run.ctx, binary)
		adminToken := initializeAndLogin(run.t, run.ctx, baseURL)
		client, err := newapi.NewClient(baseURL, []byte(adminToken), run.client)
		if err != nil {
			return err
		}
		state, err := client.ReadActualState(run.ctx)
		if err != nil {
			return err
		}
		run.sites = append(run.sites, acceptanceSite{BaseURL: baseURL, AdminToken: adminToken, Client: client})
		run.evidence.Versions[fmt.Sprintf("gateway_%d", slot+1)] = state.Version
	}
	for slot := range run.sites {
		for _, option := range []string{"ModelRatio", "CompletionRatio"} {
			value := string(mustAcceptanceJSON(map[string]int{run.values["ACCEPTANCE_PUBLIC_MODEL"]: 1}))
			if err := run.remote(slot, http.MethodPut, "/api/option/", map[string]any{"key": option, "value": value}, nil); err != nil {
				return err
			}
		}
	}
	run.evidence.Counts["sites"] = len(run.sites)
	run.evidence.Checks["independent_site_processes"] = run.sites[0].BaseURL != run.sites[1].BaseURL
	if !run.evidence.Checks["independent_site_processes"] {
		return acceptanceFault{"sites_not_independent"}
	}
	return nil
}

func (run *acceptanceRun) seedM2Deployments() error {
	for slot := range run.sites {
		code := run.state.Prefix + "-real"
		if slot == 1 {
			code = run.state.Prefix + "-local-" + uuid.NewString()[:8]
		}
		input := domain.SiteInput{
			Code: code, Name: fmt.Sprintf("%s site %d", run.state.Prefix, slot+1),
			NewAPIBaseURL: run.sites[slot].BaseURL, AccessToken: run.sites[slot].AdminToken, AdminUserID: 1,
		}
		var record acceptanceSiteRecord
		if err := run.apiRequest(http.MethodPost, "/sites", input, &record); err != nil {
			return err
		}
		run.sites[slot].ID = record.ID
	}
	run.suppliers = make([]domain.SupplierInput, acceptanceSupplierCount)
	run.supplierIDs = make([]uuid.UUID, acceptanceSupplierCount)
	for index := 0; index < acceptanceSupplierCount; index++ {
		input := domain.SupplierInput{
			Code:    fmt.Sprintf("%s-supplier-%d", run.state.Prefix, index+1),
			Name:    fmt.Sprintf("%s supplier %d", run.state.Prefix, index+1),
			BaseURL: run.values["ACCEPTANCE_SUPPLIER_BASE_URL"], APIKey: run.values["ACCEPTANCE_SUPPLIER_API_KEY"],
			Models: []domain.ModelInput{{
				Model: run.values["ACCEPTANCE_PUBLIC_MODEL"], UpstreamModel: run.values["ACCEPTANCE_UPSTREAM_MODEL"],
				InputPrice: run.values["ACCEPTANCE_INPUT_PRICE"], OutputPrice: run.values["ACCEPTANCE_OUTPUT_PRICE"],
				Currency: run.values["ACCEPTANCE_CURRENCY"], Enabled: true,
			}},
		}
		var record acceptanceSupplierRecord
		if err := run.apiRequest(http.MethodPost, "/suppliers", input, &record); err != nil {
			return err
		}
		input.APIKey = ""
		run.suppliers[index] = input
		run.supplierIDs[index] = record.ID
	}
	for index, supplierID := range run.supplierIDs {
		input := domain.DeploymentInput{
			SupplierID: supplierID,
			Sites:      []domain.DeploymentTarget{run.deploymentTarget(0, index), run.deploymentTarget(1, index)},
			Reason:     "M2 acceptance deploys each supplier record to both isolated gateways",
		}
		var result struct {
			Plans []acceptancePlanRecord `json:"plans"`
		}
		if err := run.apiRequest(http.MethodPost, "/deployments", input, &result); err != nil {
			return err
		}
		if len(result.Plans) != 2 {
			return acceptanceFault{"deployment_plan_count_invalid"}
		}
	}
	if err := run.refreshRelations(0); err != nil {
		return err
	}
	if err := run.refreshRelations(1); err != nil {
		return err
	}
	run.evidence.Counts["supplier_records"] = len(run.supplierIDs)
	run.evidence.Counts["real_upstreams"] = 1
	run.evidence.Checks["independent_supplier_records"] = len(run.supplierIDs) == acceptanceSupplierCount
	return nil
}
