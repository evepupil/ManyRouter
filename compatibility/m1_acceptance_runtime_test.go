//go:build acceptance && contract

package compatibility_test

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	supplieropenai "github.com/evepupil/ManyRouter/internal/adapters/supplier/openai"
	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/operations"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/jobs"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	httptransport "github.com/evepupil/ManyRouter/internal/transport/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (run *acceptanceRun) loadConfiguration() error {
	file, err := os.Open(filepath.Join(run.root, ".env.acceptance"))
	if err != nil {
		return acceptanceFault{"configuration_unavailable"}
	}
	defer func() { _ = file.Close() }()
	run.values = make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return acceptanceFault{"configuration_invalid"}
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.HasPrefix(value, `"`) {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				return acceptanceFault{"configuration_invalid"}
			}
			value = decoded
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		run.values[key] = value
	}
	if scanner.Err() != nil {
		return acceptanceFault{"configuration_invalid"}
	}
	for _, key := range []string{"ACCEPTANCE_NEW_API_BASE_URL", "ACCEPTANCE_NEW_API_ROOT_TOKEN", "ACCEPTANCE_SUPPLIER_BASE_URL", "ACCEPTANCE_SUPPLIER_API_KEY", "ACCEPTANCE_PUBLIC_MODEL", "ACCEPTANCE_UPSTREAM_MODEL", "ACCEPTANCE_INPUT_PRICE", "ACCEPTANCE_OUTPUT_PRICE", "ACCEPTANCE_CURRENCY", "ACCEPTANCE_SALE_RATIO"} {
		if run.values[key] == "" {
			return acceptanceFault{"configuration_incomplete"}
		}
	}
	for _, key := range []string{"ACCEPTANCE_ALLOW_NEW_API_WRITES", "ACCEPTANCE_ALLOW_REAL_UPSTREAM_REQUEST", "ACCEPTANCE_DELETE_TEMP_USER_KEY_AFTER_TEST"} {
		if run.values[key] != "true" {
			return acceptanceFault{"authorization_flag_required"}
		}
	}
	if os.Getenv("MANYROUTER_TEST_DATABASE_URL") == "" || os.Getenv("MANYROUTER_NEW_API_BINARY") == "" {
		return acceptanceFault{"test_runtime_unavailable"}
	}
	return nil
}

func (run *acceptanceRun) prepareState() error {
	run.statePath = filepath.Join(run.root, ".cache", "m1-acceptance.state.json")
	ignored := exec.Command("git", "check-ignore", "--quiet", run.statePath)
	ignored.Dir = run.root
	if err := ignored.Run(); err != nil {
		return acceptanceFault{"state_path_not_ignored"}
	}
	if err := os.MkdirAll(filepath.Dir(run.statePath), 0700); err != nil {
		return err
	}
	if err := run.acquireAcceptanceLock(); err != nil {
		return err
	}
	encoded, err := json.Marshal(run.values)
	if err != nil {
		return err
	}
	encoded = append(encoded, []byte(os.Getenv("MANYROUTER_TEST_DATABASE_URL"))...)
	digest := sha256.Sum256(encoded)
	clear(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	data, err := os.ReadFile(run.statePath)
	if err == nil {
		defer clear(data)
		if json.Unmarshal(data, &run.state) != nil || run.state.Version != 1 || run.state.Fingerprint != fingerprint {
			return acceptanceFault{"acceptance_state_mismatch"}
		}
		if !regexp.MustCompile(`^m1a_[a-f0-9]{32}$`).MatchString(run.state.Schema) {
			return acceptanceFault{"acceptance_state_invalid"}
		}
		if !regexp.MustCompile(`^m1a-[a-f0-9]{12}$`).MatchString(run.state.Prefix) {
			return acceptanceFault{"acceptance_state_invalid"}
		}
		run.restoreDedicated = run.state.RestoreDedicated
		run.restoreAuto = run.state.RestoreAuto
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	key := make([]byte, 32)
	if _, err := cryptorand.Read(key); err != nil {
		return err
	}
	defer clear(key)
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	run.state = acceptanceState{Version: 1, Schema: "m1a_" + id, Prefix: "m1a-" + id[:12], MasterKey: base64.StdEncoding.EncodeToString(key), Fingerprint: fingerprint}
	return run.saveState()
}

func (run *acceptanceRun) acquireAcceptanceLock() error {
	connection, err := pgx.Connect(run.ctx, os.Getenv("MANYROUTER_TEST_DATABASE_URL"))
	if err != nil {
		return acceptanceFault{"database_unavailable"}
	}
	scope := "manyrouter:m1:acceptance:" + strings.TrimRight(run.values["ACCEPTANCE_NEW_API_BASE_URL"], "/")
	var locked bool
	if err := connection.QueryRow(run.ctx, `SELECT pg_try_advisory_lock(hashtextextended($1::text,0))`, scope).Scan(&locked); err != nil {
		_ = connection.Close(context.Background())
		return err
	}
	if !locked {
		_ = connection.Close(context.Background())
		return acceptanceFault{"acceptance_run_locked"}
	}
	run.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		_ = connection.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1::text,0))`, scope).Scan(&released)
		_ = connection.Close(ctx)
	})
	return nil
}

func (run *acceptanceRun) saveState() error {
	data, err := json.Marshal(run.state)
	if err != nil {
		return err
	}
	defer clear(data)
	return os.WriteFile(run.statePath, data, 0600)
}

func (run *acceptanceRun) prepareDatabase() error {
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	admin, err := pgx.Connect(run.ctx, databaseURL)
	if err != nil {
		return acceptanceFault{"database_unavailable"}
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var exists bool
	if err := admin.QueryRow(run.ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, run.state.Schema).Scan(&exists); err != nil {
		return err
	}
	if !exists && run.state.SchemaReady {
		return acceptanceFault{"acceptance_schema_missing"}
	}
	if !exists {
		if _, err := admin.Exec(run.ctx, "CREATE SCHEMA "+pgx.Identifier{run.state.Schema}.Sanitize()); err != nil {
			return err
		}
	}
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
	store, err := postgres.Open(run.ctx, parsed.String())
	if err != nil {
		return err
	}
	run.store = store
	run.t.Cleanup(store.Close)
	if _, err := store.Pool().Exec(run.ctx, `CREATE TABLE IF NOT EXISTS acceptance_temp_keys (site_slot integer NOT NULL, token_name text NOT NULL, token_id bigint, deleted boolean NOT NULL DEFAULT false, PRIMARY KEY(site_slot,token_name))`); err != nil {
		return err
	}
	if _, err := store.Pool().Exec(run.ctx, `UPDATE sites SET status='disabled' WHERE code LIKE $1`, run.state.Prefix+"-local-%"); err != nil {
		return err
	}
	run.state.SchemaReady = true
	return run.saveState()
}

func (run *acceptanceRun) startBackend() error {
	vault, err := platformcrypto.NewVaultFromBase64(run.state.MasterKey, 1)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	previousLogger := slog.Default()
	slog.SetDefault(logger)
	run.t.Cleanup(func() { slog.SetDefault(previousLogger) })
	dispatcher := jobs.NewDispatcher()
	reconciler, err := reconciliation.NewService(run.store, vault, newapi.Factory{HTTPClient: run.client}, dispatcher, time.Now, uuid.New)
	if err != nil {
		return err
	}
	run.worker, err = jobs.NewClient(run.store.Pool(), reconciler, true)
	if err != nil {
		return err
	}
	if err := dispatcher.Bind(run.worker); err != nil {
		return err
	}
	onboard, err := onboarding.NewService(run.store, vault, time.Now, uuid.New)
	if err != nil {
		return err
	}
	idempotent, err := idempotency.NewService(run.store, time.Now, 24*time.Hour)
	if err != nil {
		return err
	}
	checker, err := supplieropenai.NewCredentialChecker(run.client)
	if err != nil {
		return err
	}
	service, err := operations.NewService(run.store, vault, newapi.Factory{HTTPClient: run.client}, checker)
	if err != nil {
		return err
	}
	run.apiToken = strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	authService, err := auth.NewService(run.store, run.apiToken, time.Now)
	if err != nil {
		return err
	}
	handler, err := httptransport.NewHandler(onboard, reconciler, idempotent, logger, httptransport.WithOperations(service))
	if err != nil {
		return err
	}
	router, err := httptransport.NewRouter(handler, run.apiToken, logger, httptransport.WithAuth(authService, false))
	if err != nil {
		return err
	}
	httptransport.RegisterOperationsRoutes(router, handler)
	run.api = httptest.NewServer(router)
	run.t.Cleanup(run.api.Close)
	return nil
}

func (run *acceptanceRun) stopBackend() error {
	if run.workerStopped {
		return nil
	}
	if run.workerCancel != nil {
		run.workerCancel()
	}
	if run.worker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := run.worker.Stop(ctx); err != nil {
			return err
		}
	}
	if run.api != nil {
		run.api.Close()
	}
	run.workerStopped = true
	return nil
}
