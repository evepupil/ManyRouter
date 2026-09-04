package postgres

import (
	"context"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateSupplier(ctx context.Context, data supplier.Supplier, sealed credential.Record, actorID string) error {
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
		return fmt.Errorf("create supplier credential: %w", err)
	}
	if _, err := queries.CreateSupplier(ctx, sqlc.CreateSupplierParams{
		ID: data.ID, Code: data.Code, Name: data.Name, Protocol: string(data.Protocol),
		UpstreamBaseUrl: data.UpstreamBaseURL, CredentialID: data.CredentialID,
		CredentialVersion: data.CredentialVersion, Status: string(data.Status), Version: data.Version,
		CreatedAt: databaseTime(data.CreatedAt), UpdatedAt: databaseTime(data.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("create supplier: %w", err)
	}
	for _, model := range data.Models {
		if _, err := queries.CreateSupplierModel(ctx, sqlc.CreateSupplierModelParams{
			SupplierID: data.ID, Model: model.Name, UpstreamModel: model.UpstreamName,
			InputPrice: model.InputPrice, OutputPrice: model.OutputPrice, Currency: model.Currency,
			Enabled: model.Enabled, CreatedAt: databaseTime(model.CreatedAt), UpdatedAt: databaseTime(model.UpdatedAt),
		}); err != nil {
			return fmt.Errorf("create supplier model %q: %w", model.Name, err)
		}
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorID: actorID, ObjectType: "supplier", ObjectID: data.ID.String(),
		Action: "supplier.created", Reason: "operator_request", Result: "succeeded",
		NewSummary: map[string]any{"code": data.Code, "models": len(data.Models), "status": data.Status}, CreatedAt: data.CreatedAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetSupplier(ctx context.Context, id uuid.UUID) (supplier.Supplier, error) {
	row, err := s.queries.GetSupplier(ctx, id)
	if err != nil {
		return supplier.Supplier{}, err
	}
	models, err := s.queries.ListSupplierModels(ctx, id)
	if err != nil {
		return supplier.Supplier{}, err
	}
	return mapSupplier(row, models)
}
