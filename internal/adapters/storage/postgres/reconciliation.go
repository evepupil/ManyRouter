package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateOperation(ctx context.Context, relationID, operationID uuid.UUID, now time.Time) (reconciliation.Operation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return reconciliation.Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	relation, err := queries.GetSiteSupplier(ctx, relationID)
	if err != nil {
		return reconciliation.Operation{}, err
	}
	if !relation.CurrentPlanID.Valid {
		return reconciliation.Operation{}, errors.New("site supplier has no current route plan")
	}
	row, err := queries.CreateSyncOperation(ctx, sqlc.CreateSyncOperationParams{
		ID:             operationID,
		SiteID:         relation.SiteID,
		SiteSupplierID: relation.ID,
		RoutePlanID:    uuid.UUID(relation.CurrentPlanID.Bytes),
		CreatedAt:      databaseTime(now),
	})
	if err != nil {
		return reconciliation.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reconciliation.Operation{}, err
	}
	return mapOperation(row)
}

func (s *Store) GetOperation(ctx context.Context, id uuid.UUID) (reconciliation.Operation, error) {
	row, err := s.queries.GetSyncOperation(ctx, id)
	if err != nil {
		return reconciliation.Operation{}, err
	}
	return mapOperation(row)
}

func (s *Store) LoadBundle(ctx context.Context, operationID uuid.UUID) (reconciliation.Bundle, error) {
	operationRow, err := s.queries.GetSyncOperation(ctx, operationID)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	operation, err := mapOperation(operationRow)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	siteRow, err := s.queries.GetSite(ctx, operation.SiteID)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	siteData, err := mapSite(siteRow)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	planRow, err := s.queries.GetRoutePlan(ctx, operation.RoutePlanID)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	plan, err := mapRoutePlan(planRow)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	relation, err := s.queries.GetSiteSupplier(ctx, operation.RelationID)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	adminCredentialRow, err := s.queries.GetCredential(ctx, siteData.AdminCredentialID)
	if err != nil {
		return reconciliation.Bundle{}, err
	}
	if adminCredentialRow.RevokedAt.Valid || adminCredentialRow.Purpose != string(credential.PurposeNewAPIAdmin) {
		return reconciliation.Bundle{}, errors.New("site management credential is unavailable")
	}
	bundle := reconciliation.Bundle{
		Operation:       operation,
		Site:            siteData,
		Plan:            plan,
		AdminCredential: mapCredential(adminCredentialRow),
	}
	if relation.CurrentPlanID.Valid {
		bundle.CurrentPlanID = uuid.UUID(relation.CurrentPlanID.Bytes)
	}
	if plan.Snapshot.SchemaVersion == routing.SnapshotSchemaVersion {
		latestID, err := s.queries.LatestFullSitePlanID(ctx, plan.SiteID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return reconciliation.Bundle{}, err
		}
		if err == nil {
			bundle.CurrentPlanID = latestID
		}
	}
	for _, snapshot := range plan.Snapshot.ResourceSnapshots() {
		resourceRelation, err := s.queries.GetSiteSupplier(ctx, snapshot.RelationID)
		if err != nil {
			return reconciliation.Bundle{}, err
		}
		if resourceRelation.SiteID != plan.SiteID || resourceRelation.SupplierID != snapshot.SupplierID {
			return reconciliation.Bundle{}, errors.New("route plan resource does not belong to its declared site and supplier")
		}
		channelRow, err := s.queries.GetSiteSupplierChannel(ctx, snapshot.RelationID)
		if err != nil {
			return reconciliation.Bundle{}, err
		}
		managedChannel, err := mapManagedChannel(channelRow)
		if err != nil {
			return reconciliation.Bundle{}, err
		}
		if managedChannel.ID != snapshot.Channel.ID || managedChannel.ManagedTag != snapshot.Channel.ManagedTag {
			return reconciliation.Bundle{}, errors.New("route plan channel identity does not match its stored binding")
		}
		resource := reconciliation.ResourceBundle{Snapshot: snapshot, ManagedChannel: managedChannel}
		credentialRow, err := s.queries.GetCredential(ctx, snapshot.Channel.CredentialID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return reconciliation.Bundle{}, err
		}
		if err == nil && !credentialRow.RevokedAt.Valid && credentialRow.Purpose == string(credential.PurposeSupplierAPIKey) {
			resource.SupplierCredential = mapCredential(credentialRow)
			resource.CredentialAvailable = true
		}
		bundle.Resources = append(bundle.Resources, resource)
	}
	if len(bundle.Resources) > 0 {
		bundle.ManagedChannel = bundle.Resources[0].ManagedChannel
		bundle.SupplierCredential = bundle.Resources[0].SupplierCredential
	}
	return bundle, nil
}

func (s *Store) StartOperation(ctx context.Context, operation reconciliation.Operation, step string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := queries.MarkSyncRunning(ctx, sqlc.MarkSyncRunningParams{
		ID:          operation.ID,
		CurrentStep: pgtype.Text{String: step, Valid: true},
		UpdatedAt:   databaseTime(now),
	}); err != nil {
		return err
	}
	if err := queries.SetPlanRelationsSyncing(ctx, sqlc.SetPlanRelationsSyncingParams{CurrentPlanID: databaseUUID(operation.RoutePlanID), UpdatedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.SetRoutePlanApplying(ctx, operation.RoutePlanID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordStep(ctx context.Context, record reconciliation.StepRecord) error {
	before, err := marshalJSON(record.Before)
	if err != nil {
		return err
	}
	after, err := marshalJSON(record.After)
	if err != nil {
		return err
	}
	return s.queries.SetSyncStep(ctx, sqlc.SetSyncStepParams{
		OperationID:   record.OperationID,
		StepKey:       record.Key,
		Status:        string(record.Status),
		BeforeSummary: before,
		AfterSummary:  after,
		ErrorCode:     optionalText(record.ErrorCode),
		ErrorMessage:  optionalText(record.ErrorMessage),
		StartedAt:     databaseTime(record.StartedAt),
		FinishedAt:    optionalDatabaseTime(record.FinishedAt),
	})
}

func (s *Store) BindChannel(ctx context.Context, channelID uuid.UUID, externalID int64, now time.Time) error {
	return s.queries.BindExternalChannel(ctx, sqlc.BindExternalChannelParams{
		ID:                channelID,
		ExternalChannelID: pgtype.Int8{Int64: externalID, Valid: true},
		UpdatedAt:         databaseTime(now),
	})
}

func (s *Store) CompleteOperation(ctx context.Context, bundle reconciliation.Bundle, externalChannelID int64, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := queries.ConfirmChannelBinding(ctx, sqlc.ConfirmChannelBindingParams{
		ID:                       bundle.ManagedChannel.ID,
		ExternalChannelID:        pgtype.Int8{Int64: externalChannelID, Valid: true},
		LastConfirmedPlanVersion: pgtype.Int8{Int64: bundle.Plan.Version, Valid: true},
		UpdatedAt:                databaseTime(now),
	}); err != nil {
		return err
	}
	if err := confirmResource(ctx, queries, bundle, reconciliation.ResourceConfirmation{
		Resource:          reconciliation.ResourceBundle{Snapshot: bundle.Plan.Snapshot, ManagedChannel: bundle.ManagedChannel},
		ExternalChannelID: &externalChannelID, CredentialApplied: true,
	}, now); err != nil {
		return err
	}
	if err := queries.MarkSyncSucceeded(ctx, sqlc.MarkSyncSucceededParams{ID: bundle.Operation.ID, UpdatedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.ConfirmRoutePlan(ctx, sqlc.ConfirmRoutePlanParams{ID: bundle.Plan.ID, ConfirmedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.ConfirmSiteSupplier(ctx, sqlc.ConfirmSiteSupplierParams{ID: bundle.Operation.RelationID, LastConfirmedAt: databaseTime(now)}); err != nil {
		return err
	}
	if err := queries.SetSiteCompatibility(ctx, sqlc.SetSiteCompatibilityParams{
		ID: bundle.Site.ID, CompatibilityStatus: "compatible", UpdatedAt: databaseTime(now),
	}); err != nil {
		return err
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorType:   "system",
		ActorID:     "reconciliation-worker",
		SiteID:      &bundle.Site.ID,
		ObjectType:  "route_plan",
		ObjectID:    bundle.Plan.ID.String(),
		Action:      "route_plan.confirmed",
		Reason:      bundle.Plan.Reason,
		OperationID: &bundle.Operation.ID,
		NewSummary:  map[string]any{"version": bundle.Plan.Version, "channel_id": externalChannelID},
		Result:      "succeeded",
		CreatedAt:   now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailOperation(ctx context.Context, record reconciliation.FailureRecord) error {
	operationStatus := reconciliation.OperationManualRequired
	planStatus := "failed"
	switch record.Kind {
	case reconciliation.FailureRetryable:
		operationStatus = reconciliation.OperationRetryableFailed
	case reconciliation.FailureUncertain:
		operationStatus = reconciliation.OperationUncertain
		planStatus = "uncertain"
	case reconciliation.FailureManualLock:
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := queries.MarkSyncFailed(ctx, sqlc.MarkSyncFailedParams{
		ID:               record.OperationID,
		Status:           string(operationStatus),
		CurrentStep:      optionalText(record.Step),
		LastErrorCode:    optionalText(record.Code),
		LastErrorMessage: optionalText(record.Message),
		NextAttemptAt:    optionalDatabaseTime(record.NextAttemptAt),
		UpdatedAt:        databaseTime(record.OccurredAt),
	}); err != nil {
		return err
	}
	if err := queries.SetRoutePlanFailed(ctx, sqlc.SetRoutePlanFailedParams{ID: record.RoutePlanID, Status: planStatus}); err != nil {
		return err
	}
	if err := queries.SetPlanRelationsSyncFailure(ctx, sqlc.SetPlanRelationsSyncFailureParams{
		CurrentPlanID: databaseUUID(record.RoutePlanID), SyncStatus: "failed", UpdatedAt: databaseTime(record.OccurredAt),
	}); err != nil {
		return err
	}
	if record.Kind == reconciliation.FailureManualLock {
		if err := queries.SetRelationSyncFailure(ctx, sqlc.SetRelationSyncFailureParams{
			ID: record.RelationID, SyncStatus: "manual_locked", UpdatedAt: databaseTime(record.OccurredAt), CurrentPlanID: databaseUUID(record.RoutePlanID),
		}); err != nil {
			return err
		}
	}
	if err := insertAudit(ctx, queries, auditInput{
		ActorType:   "system",
		ActorID:     "reconciliation-worker",
		SiteID:      &record.SiteID,
		ObjectType:  "route_plan",
		ObjectID:    record.RoutePlanID.String(),
		Action:      "route_plan.sync_failed",
		Reason:      record.Code,
		OperationID: &record.OperationID,
		NewSummary:  map[string]any{"kind": record.Kind, "step": record.Step},
		Result:      string(operationStatus),
		CreatedAt:   record.OccurredAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
