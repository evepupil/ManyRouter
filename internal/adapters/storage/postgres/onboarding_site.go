package postgres

import (
	"context"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateSite(ctx context.Context, data site.Site, sealed credential.Record, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if _, err := queries.CreateCredential(ctx, sqlc.CreateCredentialParams{
		ID: sealed.ID, Purpose: string(sealed.Purpose), Ciphertext: sealed.Ciphertext,
		Nonce: sealed.Nonce, KeyVersion: sealed.KeyVersion, CreatedAt: databaseTime(data.CreatedAt),
	}); err != nil {
		return fmt.Errorf("create site credential: %w", err)
	}
	if _, err := queries.CreateSite(ctx, sqlc.CreateSiteParams{
		ID: data.ID, Code: data.Code, Name: data.Name, NewApiBaseUrl: data.NewAPIBaseURL,
		AdminCredentialID: data.AdminCredentialID, Status: string(data.Status),
		CompatibilityStatus: string(data.CompatibilityStatus), Version: data.Version,
		CreatedAt: databaseTime(data.CreatedAt), UpdatedAt: databaseTime(data.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("create site: %w", err)
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorID: actorID, SiteID: &data.ID, ObjectType: "site", ObjectID: data.ID.String(),
		Action: "site.created", Reason: "operator_request", Result: "succeeded",
		NewSummary: map[string]any{"code": data.Code, "status": data.Status}, CreatedAt: data.CreatedAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetSite(ctx context.Context, id uuid.UUID) (site.Site, error) {
	row, err := s.queries.GetSite(ctx, id)
	if err != nil {
		return site.Site{}, err
	}
	return mapSite(row)
}
