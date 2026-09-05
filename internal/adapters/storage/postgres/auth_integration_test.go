//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestAuthPersistenceAndConcurrentInitialization(t *testing.T) {
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := pgx.Identifier{"auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*config.ConnConfig)
	defer func() { _ = database.Close() }()
	if err := runMigrations(ctx, database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &Store{pool: pool, queries: sqlc.New(pool)}
	now := time.Now().UTC()
	results := make(chan struct {
		created  bool
		operator auth.Operator
		err      error
	}, 2)
	for _, name := range []string{"first", "second"} {
		operator := auth.Operator{User: auth.User{ID: uuid.New(), Username: name, Role: "owner"}, PasswordHash: "fixture-hash", Enabled: true, CreatedAt: now}
		go func() {
			created, err := store.CreateInitialOperator(ctx, operator)
			results <- struct {
				created  bool
				operator auth.Operator
				err      error
			}{created, operator, err}
		}()
	}
	createdCount := 0
	var owner auth.Operator
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
			owner = result.operator
		}
	}
	if createdCount != 1 {
		t.Fatalf("created %d owners", createdCount)
	}
	if initialized, err := store.AuthInitialized(ctx); err != nil || !initialized {
		t.Fatalf("initialization state: %v", err)
	}
	loadedOwner, err := store.FindOperator(ctx, owner.User.Username)
	if err != nil || loadedOwner == nil || loadedOwner.User.ID != owner.User.ID {
		t.Fatalf("owner persistence: %v", err)
	}
	record := auth.SessionRecord{TokenHash: strings.Repeat("a", 64), User: owner.User, CSRFHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour), CreatedAt: now, Enabled: true}
	if err := store.SaveOperatorSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	loadedSession, err := store.FindOperatorSession(ctx, record.TokenHash)
	if err != nil || loadedSession == nil || loadedSession.User.ID != owner.User.ID || loadedSession.CSRFHash != record.CSRFHash {
		t.Fatalf("session persistence: %v", err)
	}
	if err := store.DeleteOperatorSession(ctx, record.TokenHash); err != nil {
		t.Fatal(err)
	}
	if session, err := store.FindOperatorSession(ctx, record.TokenHash); err != nil || session != nil {
		t.Fatalf("session revocation: %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_events WHERE object_type = 'operator'").Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("authentication audits: %d, %v", auditCount, err)
	}

	counts := make(chan struct {
		count int32
		err   error
	}, 15)
	for range 15 {
		go func() {
			count, err := store.ConsumeAuthAttempt(ctx, "fixture-attempt", now, now.Add(-15*time.Minute))
			counts <- struct {
				count int32
				err   error
			}{count, err}
		}()
	}
	seen := map[int32]bool{}
	for range 15 {
		result := <-counts
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.count < 1 || result.count > 15 || seen[result.count] {
			t.Fatalf("non-atomic count: %d", result.count)
		}
		seen[result.count] = true
	}
	if count, err := store.ConsumeAuthAttempt(ctx, "fixture-attempt", now.Add(15*time.Minute), now); err != nil || count != 1 {
		t.Fatalf("attempt window reset: %d, %v", count, err)
	}
}
