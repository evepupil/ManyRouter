package postgres

import (
	"context"
	"slices"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) SupersedeOperation(ctx context.Context, bundle reconciliation.Bundle, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := queries.MarkSyncSuperseded(ctx, sqlc.MarkSyncSupersededParams{ID: bundle.Operation.ID, UpdatedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.SetRoutePlanFailed(ctx, sqlc.SetRoutePlanFailedParams{ID: bundle.Plan.ID, Status: string(routing.PlanSuperseded)}); err != nil {
		return err
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorType: "system", ActorID: "reconciliation-worker", SiteID: &bundle.Site.ID,
		ObjectType: "route_plan", ObjectID: bundle.Plan.ID.String(), Action: "route_plan.superseded",
		Reason: "newer_site_plan", OperationID: &bundle.Operation.ID, Result: "superseded", CreatedAt: now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ConfirmResource(ctx context.Context, bundle reconciliation.Bundle, resource reconciliation.ResourceConfirmation, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := confirmResource(ctx, s.queries.WithTx(tx), bundle, resource, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func confirmResource(ctx context.Context, queries *sqlc.Queries, bundle reconciliation.Bundle, resource reconciliation.ResourceConfirmation, now time.Time) error {
	externalID := pgtype.Int8{}
	if resource.ExternalChannelID != nil {
		externalID = pgtype.Int8{Int64: *resource.ExternalChannelID, Valid: true}
	}
	desired := resource.Resource.Snapshot.Channel
	if err := queries.ConfirmM1Channel(ctx, sqlc.ConfirmM1ChannelParams{
		ID: resource.Resource.ManagedChannel.ID, ExternalChannelID: externalID,
		PlanVersion:       pgtype.Int8{Int64: bundle.Plan.Version, Valid: true},
		Enabled:           desired.DesiredStatus == routing.DesiredEnabled,
		CredentialApplied: resource.CredentialApplied,
		CredentialID:      databaseUUID(desired.CredentialID),
		CredentialVersion: pgtype.Int4{Int32: desired.CredentialVersion, Valid: true},
		UpdatedAt:         databaseTime(now),
	}); err != nil {
		return err
	}
	return queries.ConfirmSiteSupplier(ctx, sqlc.ConfirmSiteSupplierParams{ID: resource.Resource.Snapshot.RelationID, LastConfirmedAt: databaseTime(now)})
}

func (s *Store) ConfirmSitePrices(ctx context.Context, bundle reconciliation.Bundle, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	for _, id := range bundle.Plan.Snapshot.PriceVersionIDs {
		if err := queries.MarkSitePriceApplied(ctx, sqlc.MarkSitePriceAppliedParams{
			ID: id, SiteID: bundle.Site.ID, AppliedAt: databaseTime(now), RoutePlanID: databaseUUID(bundle.Plan.ID),
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteSiteOperation(ctx context.Context, bundle reconciliation.Bundle, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	supplierIDs := make([]uuid.UUID, 0, len(bundle.Resources))
	for _, resource := range bundle.Resources {
		supplierIDs = append(supplierIDs, resource.Snapshot.SupplierID)
	}
	slices.SortFunc(supplierIDs, func(left, right uuid.UUID) int { return slices.Compare(left[:], right[:]) })
	for _, id := range slices.Compact(supplierIDs) {
		if _, err := queries.LockSupplierRotation(ctx, id); err != nil {
			return err
		}
		if err := queries.PromoteConfirmedSupplierCredential(ctx, sqlc.PromoteConfirmedSupplierCredentialParams{ID: id, UpdatedAt: databaseTime(now)}); err != nil {
			return err
		}
	}
	if err := queries.MarkSyncSucceeded(ctx, sqlc.MarkSyncSucceededParams{ID: bundle.Operation.ID, UpdatedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.ConfirmRoutePlan(ctx, sqlc.ConfirmRoutePlanParams{ID: bundle.Plan.ID, ConfirmedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.SetSiteCompatibility(ctx, sqlc.SetSiteCompatibilityParams{
		ID: bundle.Site.ID, CompatibilityStatus: "compatible", UpdatedAt: databaseTime(now),
	}); err != nil {
		return err
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorType: "system", ActorID: "reconciliation-worker", SiteID: &bundle.Site.ID,
		ObjectType: "route_plan", ObjectID: bundle.Plan.ID.String(), Action: "route_plan.confirmed",
		Reason: bundle.Plan.Reason, OperationID: &bundle.Operation.ID, Result: "succeeded", CreatedAt: now,
		NewSummary: map[string]any{"version": bundle.Plan.Version, "resources": len(bundle.Resources)},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
