//go:build acceptance && contract

package compatibility_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type acceptanceFault struct{ code string }

func (err acceptanceFault) Error() string { return err.code }

type acceptanceEvidence struct {
	Version  int               `json:"version"`
	Versions map[string]string `json:"versions"`
	Counts   map[string]int    `json:"counts"`
	Checks   map[string]bool   `json:"checks"`
	Stages   []acceptanceStage `json:"stages"`
}
type acceptanceStage struct {
	Phase     string `json:"phase"`
	Passed    bool   `json:"passed"`
	ErrorCode string `json:"error_code,omitempty"`
}

// This ignored local state contains the test vault key. It never enters evidence.
type acceptanceState struct {
	Version          int    `json:"version"`
	Schema           string `json:"schema"`
	Prefix           string `json:"prefix"`
	MasterKey        string `json:"master_key"`
	Fingerprint      string `json:"fingerprint"`
	SchemaReady      bool   `json:"schema_ready"`
	RestoreDedicated bool   `json:"restore_dedicated"`
	RestoreAuto      bool   `json:"restore_auto"`
}

type acceptanceRelation struct {
	ID            uuid.UUID `json:"id"`
	SiteID        uuid.UUID `json:"site_id"`
	SupplierID    uuid.UUID `json:"supplier_id"`
	GroupKey      string    `json:"group_key"`
	DisplayName   string    `json:"group_display_name"`
	Version       int64     `json:"version"`
	CurrentPlanID uuid.UUID `json:"current_plan_id"`
}
type acceptanceSite struct {
	ID           uuid.UUID
	BaseURL      string
	AdminToken   string
	Client       *newapi.Client
	Relations    []acceptanceRelation
	AutoKey      string
	DedicatedKey string
	AutoGroup    string
}
type acceptanceTempKey struct {
	Site      int
	Name      string
	ID        int64
	Key       string
	Uncertain bool
	Deleted   bool
}

type acceptanceRun struct {
	t                *testing.T
	ctx              context.Context
	root             string
	values           map[string]string
	state            acceptanceState
	statePath        string
	evidencePath     string
	evidence         acceptanceEvidence
	store            *postgres.Store
	api              *httptest.Server
	apiToken         string
	worker           *river.Client[pgx.Tx]
	workerCancel     context.CancelFunc
	workerStopped    bool
	client           *http.Client
	sites            []acceptanceSite
	suppliers        []domain.SupplierInput
	supplierIDs      []uuid.UUID
	keys             []*acceptanceTempKey
	realKeyBaseline  map[int64]struct{}
	cleanupErrors    []error
	baseline         reconciliation.ActualState
	baselineOptions  map[string][32]byte
	restoreDedicated bool
	restoreAuto      bool
	seedPlanVersions [2]int64
	ownedTags        map[string]bool
	failLocal        atomic.Bool
}

func TestM1RealAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal("acceptance workspace unavailable")
	}
	run := &acceptanceRun{t: t, ctx: ctx, root: root, client: &http.Client{Timeout: 90 * time.Second}, ownedTags: make(map[string]bool)}
	run.evidence = acceptanceEvidence{Version: 1, Versions: make(map[string]string), Counts: make(map[string]int), Checks: make(map[string]bool), Stages: make([]acceptanceStage, 0)}
	run.evidencePath = filepath.Join(root, ".cache", "m1-acceptance-"+time.Now().UTC().Format("20060102150405.000000000")+".evidence.json")
	defer run.finish()
	run.must("configuration", run.loadConfiguration())
	run.must("persistent_state", run.prepareState())
	run.must("database", run.prepareDatabase())
	run.must("gateway_preflight", run.prepareGateways())
	run.must("http_backend", run.startBackend())
	run.must("suppliers_and_deployments", run.seedDeployments())
	workerContext, stopWorker := context.WithCancel(context.Background())
	run.workerCancel = stopWorker
	run.must("start_worker", run.worker.Start(workerContext))
	run.must("initial_real_sync", run.waitSite(0, false))
	run.must("initial_local_sync", run.waitSite(1, false))
	run.must("manual_strategies", run.checkedScenario(run.configureStrategies))
	run.must("recover_interrupted_entry_access", run.confirmEntryAccessRecovered())
	run.must("user_requests", run.exerciseUserRequests())
	run.must("price_and_membership_isolation", run.checkedScenario(run.exerciseIsolation))
	run.must("single_site_failure", run.checkedScenario(run.exerciseFailure))
	run.must("repeat_synchronization", run.checkedScenario(run.exerciseRepeat))
	run.must("pause_temporary_site", run.pauseTemporarySite())
	run.must("stop_backend", run.stopBackend())
	run.must("independent_gateway_calls", run.exerciseStoppedBackend())
	run.must("preserve_existing_resources", run.verifyPreserved())
	run.evidence.Checks["main_chain_completed"] = true
}

func (run *acceptanceRun) must(phase string, err error) {
	stage := acceptanceStage{Phase: phase, Passed: err == nil}
	if err != nil {
		stage.ErrorCode = acceptanceErrorCode(err)
	}
	run.evidence.Stages = append(run.evidence.Stages, stage)
	_ = run.writeEvidence()
	if err != nil {
		run.t.Fatalf("acceptance stopped at %s (%s)", phase, stage.ErrorCode)
	}
	run.t.Logf("acceptance stage passed: %s", phase)
}

func acceptanceErrorCode(err error) string {
	var fault acceptanceFault
	if errors.As(err, &fault) {
		return fault.code
	}
	var gateway *reconciliation.Failure
	if errors.As(err, &gateway) && regexp.MustCompile(`^[a-z0-9_]{1,80}$`).MatchString(gateway.Code) {
		return gateway.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, domain.ErrBusy) {
		return "configuration_busy"
	}
	return "operation_failed"
}

func (run *acceptanceRun) finish() {
	entryErr := run.restoreEntryAccess()
	entryStage := acceptanceStage{Phase: "cleanup_entry_access", Passed: entryErr == nil}
	if entryErr != nil {
		entryStage.ErrorCode = acceptanceErrorCode(entryErr)
		run.t.Error("acceptance entry access restoration was not confirmed")
	}
	run.evidence.Stages = append(run.evidence.Stages, entryStage)
	if run.api != nil && !run.workerStopped {
		pauseErr := run.pauseTemporarySite()
		pauseStage := acceptanceStage{Phase: "cleanup_temp_site", Passed: pauseErr == nil}
		if pauseErr != nil {
			pauseStage.ErrorCode = acceptanceErrorCode(pauseErr)
			run.t.Error("acceptance temporary site pause was not confirmed")
		}
		run.evidence.Stages = append(run.evidence.Stages, pauseStage)
	}
	if err := run.stopBackend(); err != nil {
		run.evidence.Stages = append(run.evidence.Stages, acceptanceStage{Phase: "cleanup_worker", ErrorCode: acceptanceErrorCode(err)})
	}
	if err := run.cleanupKeys(); err != nil {
		run.evidence.Stages = append(run.evidence.Stages, acceptanceStage{Phase: "cleanup_keys", ErrorCode: acceptanceErrorCode(err)})
		run.t.Error("acceptance temporary key cleanup was not confirmed")
	}
	run.evidence.Checks["passed"] = run.evidence.Checks["main_chain_completed"] && run.evidence.Checks["temporary_keys_removed"] && !run.t.Failed()
	if err := run.writeEvidence(); err != nil {
		run.t.Error("acceptance evidence could not be written")
	}
}

func (run *acceptanceRun) writeEvidence() error {
	if run.evidencePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(run.evidence, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(run.evidencePath), 0700); err != nil {
		return err
	}
	return os.WriteFile(run.evidencePath, data, 0600)
}
