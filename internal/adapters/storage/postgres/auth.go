package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) AuthInitialized(ctx context.Context) (bool, error) {
	return s.queries.AuthInitialized(ctx)
}

func (s *Store) CreateInitialOperator(ctx context.Context, operator auth.Operator) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := s.queries.WithTx(tx)
	if err := queries.LockOperatorSetup(ctx); err != nil {
		return false, err
	}
	initialized, err := queries.AuthInitialized(ctx)
	if err != nil || initialized {
		return false, err
	}
	if err := queries.CreateInitialOperator(ctx, sqlc.CreateInitialOperatorParams{
		ID: operator.User.ID, Username: operator.User.Username, PasswordHash: operator.PasswordHash,
		Role: operator.User.Role, Enabled: operator.Enabled, CreatedAt: databaseTime(operator.CreatedAt),
	}); err != nil {
		return false, err
	}
	if err := insertAuthAudit(ctx, queries, operator.User.ID, "operator.setup", operator.CreatedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) FindOperator(ctx context.Context, username string) (*auth.Operator, error) {
	row, err := s.queries.FindOperator(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &auth.Operator{User: auth.User{ID: row.ID, Username: row.Username, Role: row.Role}, PasswordHash: row.PasswordHash, Enabled: row.Enabled, CreatedAt: row.CreatedAt.Time}, nil
}

func (s *Store) SaveOperatorSession(ctx context.Context, record auth.SessionRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := s.queries.WithTx(tx)
	if _, err := tx.Exec(ctx, `DELETE FROM operator_sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	if err := queries.SaveOperatorSession(ctx, sqlc.SaveOperatorSessionParams{TokenHash: record.TokenHash, OperatorID: record.User.ID, CsrfHash: record.CSRFHash, ExpiresAt: databaseTime(record.ExpiresAt), CreatedAt: databaseTime(record.CreatedAt)}); err != nil {
		return err
	}
	if err := insertAuthAudit(ctx, queries, record.User.ID, "operator.login", record.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FindOperatorSession(ctx context.Context, tokenHash string) (*auth.SessionRecord, error) {
	row, err := s.queries.FindOperatorSession(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &auth.SessionRecord{TokenHash: row.TokenHash, User: auth.User{ID: row.ID, Username: row.Username, Role: row.Role}, CSRFHash: row.CsrfHash, ExpiresAt: row.ExpiresAt.Time, CreatedAt: row.CreatedAt.Time, Enabled: row.Enabled}, nil
}

func (s *Store) DeleteOperatorSession(ctx context.Context, tokenHash string) error {
	return s.queries.DeleteOperatorSession(ctx, sqlc.DeleteOperatorSessionParams{TokenHash: tokenHash, ID: uuid.New(), CreatedAt: databaseTime(time.Now())})
}

func (s *Store) ConsumeAuthAttempt(ctx context.Context, key string, now, cutoff time.Time) (int32, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM auth_login_attempts WHERE window_start <= $1`, cutoff.UTC()); err != nil {
		return 0, err
	}
	return s.queries.ConsumeAuthAttempt(ctx, sqlc.ConsumeAuthAttemptParams{Key: key, NowAt: databaseTime(now), Cutoff: databaseTime(cutoff)})
}

func insertAuthAudit(ctx context.Context, queries *sqlc.Queries, operatorID uuid.UUID, action string, now time.Time) error {
	return queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		ID: uuid.New(), ActorType: "operator", ActorID: operatorID.String(), ObjectType: "operator",
		ObjectID: operatorID.String(), Action: action, Reason: action, Result: "succeeded", CreatedAt: databaseTime(now),
	})
}
