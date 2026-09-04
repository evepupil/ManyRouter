package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/jackc/pgx/v5"
)

func (s *Store) FindIdempotencyRecord(ctx context.Context, scope, key string, now time.Time) (*idempotency.Record, error) {
	row, err := s.queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		Scope:          scope,
		IdempotencyKey: key,
		ExpiresAt:      databaseTime(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return mapIdempotencyRecord(row)
}

func (s *Store) SaveIdempotencyRecord(ctx context.Context, record idempotency.Record) error {
	_, err := s.queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		Scope:          record.Scope,
		IdempotencyKey: record.Key,
		RequestHash:    record.RequestHash,
		StatusCode:     int32(record.StatusCode),
		ResponseBody:   record.Response,
		CreatedAt:      databaseTime(record.CreatedAt),
		ExpiresAt:      databaseTime(record.ExpiresAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func mapIdempotencyRecord(row sqlc.IdempotencyRecord) (*idempotency.Record, error) {
	if !row.CreatedAt.Valid || !row.ExpiresAt.Valid {
		return nil, errors.New("idempotency timestamps are invalid")
	}
	return &idempotency.Record{
		Scope:       row.Scope,
		Key:         row.IdempotencyKey,
		RequestHash: row.RequestHash,
		StatusCode:  int(row.StatusCode),
		Response:    append([]byte(nil), row.ResponseBody...),
		CreatedAt:   row.CreatedAt.Time.UTC(),
		ExpiresAt:   row.ExpiresAt.Time.UTC(),
	}, nil
}
