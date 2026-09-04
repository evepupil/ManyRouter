package postgres

import (
	"context"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateRelationAndPlan(
	ctx context.Context,
	relation routing.Relation,
	channel routing.ManagedChannel,
	planID uuid.UUID,
	snapshot routing.Snapshot,
	payload []byte,
	contentHash string,
	reason string,
	actorID string,
) (routing.Relation, routing.Plan, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := queries.LockSitePlanVersion(ctx, relation.SiteID.String()); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("lock site route plan: %w", err)
	}
	if _, err := queries.CreateSiteSupplier(ctx, sqlc.CreateSiteSupplierParams{
		ID: relation.ID, SiteID: relation.SiteID, SupplierID: relation.SupplierID,
		GroupKey: relation.GroupKey, GroupDisplayName: relation.GroupDisplayName, SaleRatio: relation.SaleRatio,
		Visible: relation.Visible, DesiredStatus: string(relation.DesiredStatus), SyncStatus: string(relation.SyncStatus),
		Version: relation.Version, CreatedAt: databaseTime(relation.CreatedAt), UpdatedAt: databaseTime(relation.UpdatedAt),
	}); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("create site supplier: %w", err)
	}
	if _, err := queries.CreateSiteSupplierChannel(ctx, sqlc.CreateSiteSupplierChannelParams{
		ID: channel.ID, SiteSupplierID: channel.RelationID, ManagedTag: channel.ManagedTag,
		CreatedAt: databaseTime(channel.CreatedAt), UpdatedAt: databaseTime(channel.UpdatedAt),
	}); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("create managed channel: %w", err)
	}
	maxVersion, err := queries.GetMaxSitePlanVersion(ctx, relation.SiteID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("allocate route plan version: %w", err)
	}
	nextVersion := maxVersion + 1
	planRow, err := queries.CreateRoutePlan(ctx, sqlc.CreateRoutePlanParams{
		ID: planID, SiteID: relation.SiteID, SiteSupplierID: relation.ID, Version: nextVersion,
		PreviousPlanID: pgtype.UUID{}, Reason: reason, Snapshot: payload, ContentHash: contentHash,
		Status: string(routing.PlanPending), CreatedAt: databaseTime(relation.CreatedAt),
	})
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("create route plan: %w", err)
	}
	if err := queries.SetCurrentRoutePlan(ctx, sqlc.SetCurrentRoutePlanParams{
		ID: relation.ID, CurrentPlanID: databaseUUID(planID), UpdatedAt: databaseTime(relation.UpdatedAt),
	}); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("set current route plan: %w", err)
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorID: actorID, SiteID: &relation.SiteID, ObjectType: "site_supplier", ObjectID: relation.ID.String(),
		Action: "site_supplier.created", Reason: reason, Result: "succeeded",
		NewSummary: map[string]any{"group_key": relation.GroupKey, "route_plan_version": nextVersion}, CreatedAt: relation.CreatedAt,
	}); err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	relation.CurrentPlanID = planID
	relation.CurrentPlanVersion = nextVersion
	relation.Version++
	plan := routing.Plan{
		ID: planRow.ID, SiteID: planRow.SiteID, RelationID: planRow.SiteSupplierID, Version: planRow.Version,
		Reason: planRow.Reason, Snapshot: snapshot, SnapshotJSON: append([]byte(nil), planRow.Snapshot...),
		ContentHash: planRow.ContentHash, Status: routing.PlanStatus(planRow.Status), CreatedAt: planRow.CreatedAt.Time.UTC(),
	}
	return relation, plan, nil
}

func (s *Store) GetRelation(ctx context.Context, id uuid.UUID) (routing.Relation, error) {
	row, err := s.queries.GetSiteSupplierDetails(ctx, id)
	if err != nil {
		return routing.Relation{}, err
	}
	return mapRelation(row)
}
