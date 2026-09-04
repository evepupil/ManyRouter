package postgres

import (
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func databaseTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func databaseUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: value != uuid.Nil}
}

func optionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}

func mapSite(row sqlc.Site) (site.Site, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return site.Site{}, errors.New("site timestamps are invalid")
	}
	return site.Site{
		ID:                  row.ID,
		Code:                row.Code,
		Name:                row.Name,
		NewAPIBaseURL:       row.NewApiBaseUrl,
		AdminCredentialID:   row.AdminCredentialID,
		Status:              site.Status(row.Status),
		CompatibilityStatus: site.CompatibilityStatus(row.CompatibilityStatus),
		Version:             row.Version,
		CreatedAt:           row.CreatedAt.Time.UTC(),
		UpdatedAt:           row.UpdatedAt.Time.UTC(),
	}, nil
}

func mapSupplier(row sqlc.Supplier, modelRows []sqlc.SupplierModel) (supplier.Supplier, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return supplier.Supplier{}, errors.New("supplier timestamps are invalid")
	}
	models := make([]supplier.Model, 0, len(modelRows))
	for _, model := range modelRows {
		if !model.CreatedAt.Valid || !model.UpdatedAt.Valid {
			return supplier.Supplier{}, errors.New("supplier model timestamps are invalid")
		}
		models = append(models, supplier.Model{
			Name:         model.Model,
			UpstreamName: model.UpstreamModel,
			InputPrice:   model.InputPrice,
			OutputPrice:  model.OutputPrice,
			Currency:     model.Currency,
			Enabled:      model.Enabled,
			CreatedAt:    model.CreatedAt.Time.UTC(),
			UpdatedAt:    model.UpdatedAt.Time.UTC(),
		})
	}
	return supplier.Supplier{
		ID:                row.ID,
		Code:              row.Code,
		Name:              row.Name,
		Protocol:          supplier.Protocol(row.Protocol),
		UpstreamBaseURL:   row.UpstreamBaseUrl,
		CredentialID:      row.CredentialID,
		CredentialVersion: row.CredentialVersion,
		Status:            supplier.Status(row.Status),
		Version:           row.Version,
		Models:            models,
		CreatedAt:         row.CreatedAt.Time.UTC(),
		UpdatedAt:         row.UpdatedAt.Time.UTC(),
	}, nil
}

func mapCredential(row sqlc.Credential) credential.Record {
	return credential.Record{
		ID:         row.ID,
		Purpose:    credential.Purpose(row.Purpose),
		Ciphertext: append([]byte(nil), row.Ciphertext...),
		Nonce:      append([]byte(nil), row.Nonce...),
		KeyVersion: row.KeyVersion,
	}
}

func mapRoutePlan(row sqlc.RoutePlanVersion) (routing.Plan, error) {
	if !row.CreatedAt.Valid {
		return routing.Plan{}, errors.New("route plan created time is invalid")
	}
	snapshot, err := routing.DecodeSnapshot(row.Snapshot)
	if err != nil {
		return routing.Plan{}, err
	}
	return routing.Plan{
		ID:             row.ID,
		SiteID:         row.SiteID,
		RelationID:     row.SiteSupplierID,
		Version:        row.Version,
		PreviousPlanID: optionalUUID(row.PreviousPlanID),
		Reason:         row.Reason,
		Snapshot:       snapshot,
		SnapshotJSON:   append([]byte(nil), row.Snapshot...),
		ContentHash:    row.ContentHash,
		Status:         routing.PlanStatus(row.Status),
		CreatedAt:      row.CreatedAt.Time.UTC(),
		ConfirmedAt:    optionalTime(row.ConfirmedAt),
	}, nil
}

func mapManagedChannel(row sqlc.SiteSupplierChannel) (routing.ManagedChannel, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return routing.ManagedChannel{}, errors.New("managed channel timestamps are invalid")
	}
	var externalID *int64
	if row.ExternalChannelID.Valid {
		value := row.ExternalChannelID.Int64
		externalID = &value
	}
	var confirmedVersion *int64
	if row.LastConfirmedPlanVersion.Valid {
		value := row.LastConfirmedPlanVersion.Int64
		confirmedVersion = &value
	}
	return routing.ManagedChannel{
		ID:                       row.ID,
		RelationID:               row.SiteSupplierID,
		ManagedTag:               row.ManagedTag,
		ExternalChannelID:        externalID,
		LastConfirmedPlanVersion: confirmedVersion,
		CreatedAt:                row.CreatedAt.Time.UTC(),
		UpdatedAt:                row.UpdatedAt.Time.UTC(),
	}, nil
}

func mapRelation(row sqlc.GetSiteSupplierDetailsRow) (routing.Relation, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return routing.Relation{}, errors.New("site supplier timestamps are invalid")
	}
	currentPlanID := uuid.Nil
	if planID := optionalUUID(row.CurrentPlanID); planID != nil {
		currentPlanID = *planID
	}
	return routing.Relation{
		ID:                 row.ID,
		SiteID:             row.SiteID,
		SupplierID:         row.SupplierID,
		GroupKey:           row.GroupKey,
		GroupDisplayName:   row.GroupDisplayName,
		SaleRatio:          row.SaleRatio,
		Visible:            row.Visible,
		DesiredStatus:      routing.DesiredStatus(row.DesiredStatus),
		SyncStatus:         routing.SyncStatus(row.SyncStatus),
		Version:            row.Version,
		CurrentPlanID:      currentPlanID,
		CurrentPlanVersion: row.RoutePlanVersion,
		LastConfirmedAt:    optionalTime(row.LastConfirmedAt),
		CreatedAt:          row.CreatedAt.Time.UTC(),
		UpdatedAt:          row.UpdatedAt.Time.UTC(),
	}, nil
}
