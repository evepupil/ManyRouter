package postgres

import (
	"context"
	"errors"
	"time"

	catalogapp "github.com/evepupil/ManyRouter/internal/application/catalog"
	operationsdomain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) CreateProductToken(ctx context.Context, record catalogapp.TokenRecord) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO site_product_tokens(
			id,site_id,token_hash,status,reason,created_by,created_at
		) VALUES($1,$2,$3,'active',$4,$5,$6)
	`, record.ID, record.SiteID, record.TokenHash, record.Reason, record.CreatedBy, record.CreatedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(id,actor_type,actor_id,site_id,object_type,object_id,action,reason,result,created_at)
		VALUES($1,'operator',$2,$3,'site_product_token',$4,'create_product_token',$5,'succeeded',$6)
	`, uuid.New(), record.CreatedBy, record.SiteID, record.ID.String(), record.Reason, record.CreatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) ListProductTokens(ctx context.Context, siteID uuid.UUID) ([]catalogapp.TokenRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id,site_id,status,reason,created_by,created_at,last_used_at,revoked_at
		FROM site_product_tokens
		WHERE site_id=$1
		ORDER BY created_at DESC,id DESC
	`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]catalogapp.TokenRecord, 0)
	for rows.Next() {
		var record catalogapp.TokenRecord
		var lastUsed, revoked pgtype.Timestamptz
		if err := rows.Scan(
			&record.ID, &record.SiteID, &record.Status, &record.Reason, &record.CreatedBy,
			&record.CreatedAt, &lastUsed, &revoked,
		); err != nil {
			return nil, err
		}
		record.CreatedAt = record.CreatedAt.UTC()
		record.LastUsedAt = optionalCatalogTime(lastUsed)
		record.RevokedAt = optionalCatalogTime(revoked)
		result = append(result, record)
	}
	return result, rows.Err()
}

func (store *Store) RevokeProductToken(ctx context.Context, siteID, tokenID uuid.UUID, reason, actor string, revokedAt time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE site_product_tokens
		SET status='revoked',revoked_at=$3
		WHERE id=$1 AND site_id=$2 AND status='active'
	`, tokenID, siteID, revokedAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return operationsdomain.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(id,actor_type,actor_id,site_id,object_type,object_id,action,reason,result,created_at)
		VALUES($1,'operator',$2,$3,'site_product_token',$4,'revoke_product_token',$5,'succeeded',$6)
	`, uuid.New(), actor, siteID, tokenID.String(), reason, revokedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) AuthenticateProductToken(ctx context.Context, tokenHash string, usedAt time.Time) (uuid.UUID, error) {
	var siteID uuid.UUID
	err := store.pool.QueryRow(ctx, `
		UPDATE site_product_tokens token
		SET last_used_at=$2
		FROM sites site
		WHERE token.token_hash=$1 AND token.status='active'
		  AND site.id=token.site_id AND site.status='enabled'
		RETURNING token.site_id
	`, tokenHash, usedAt.UTC()).Scan(&siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, catalogapp.ErrUnauthorized
	}
	return siteID, err
}

func optionalCatalogTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

var _ catalogapp.Repository = (*Store)(nil)
