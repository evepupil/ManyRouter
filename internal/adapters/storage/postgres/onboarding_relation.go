package postgres

import (
	"bytes"
	"context"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	domainoperations "github.com/evepupil/ManyRouter/internal/domain/operations"
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
	locked, err := tryTransactionLock(ctx, tx, "manyrouter_operator_configuration", 2)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if !locked {
		return routing.Relation{}, routing.Plan{}, domainoperations.ErrBusy
	}
	locked, err = tryTransactionLock(ctx, tx, relation.SiteID.String(), 1)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if !locked {
		return routing.Relation{}, routing.Plan{}, domainoperations.ErrBusy
	}
	var hasSitePlan bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM route_plan_versions WHERE site_id=$1 AND snapshot->>'schema_version'='2')`, relation.SiteID).Scan(&hasSitePlan); err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if hasSitePlan {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("%w: this site uses whole-site plans; use the operations deployment endpoint", onboarding.ErrInvalidInput)
	}
	siteRow, err := queries.GetSite(ctx, relation.SiteID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	currentSite, err := mapSite(siteRow)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if err = currentSite.CanSync(); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("%w: site is no longer available for synchronization", onboarding.ErrInvalidInput)
	}
	supplierRow, err := queries.GetSupplier(ctx, relation.SupplierID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if supplierRow.PendingCredentialID.Valid {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("%w: supplier credential rotation must finish before legacy deployment", onboarding.ErrInvalidInput)
	}
	modelRows, err := queries.ListSupplierModels(ctx, relation.SupplierID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	currentSupplier, err := mapSupplier(supplierRow, modelRows)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if err = currentSupplier.CanDeploy(); err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("%w: supplier is no longer available for deployment", onboarding.ErrInvalidInput)
	}
	currentSnapshot, err := routing.BuildSnapshot(currentSite, currentSupplier, relation, channel)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	currentPayload, currentContentHash, err := routing.EncodeSnapshot(currentSnapshot)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, err
	}
	if currentContentHash != contentHash || !bytes.Equal(currentPayload, payload) {
		return routing.Relation{}, routing.Plan{}, domainoperations.ErrConflict
	}
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
