package routing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type DesiredStatus string

const (
	DesiredObserving DesiredStatus = "observing"
	DesiredEnabled   DesiredStatus = "enabled"
	DesiredDisabled  DesiredStatus = "disabled"
)

type SyncStatus string

const (
	SyncPending      SyncStatus = "pending"
	Syncing          SyncStatus = "syncing"
	SyncActive       SyncStatus = "active"
	SyncFailed       SyncStatus = "failed"
	SyncManualLocked SyncStatus = "manual_locked"
)

type Relation struct {
	ID                 uuid.UUID
	SiteID             uuid.UUID
	SupplierID         uuid.UUID
	GroupKey           string
	GroupDisplayName   string
	SaleRatio          decimal.Decimal
	Visible            bool
	DesiredStatus      DesiredStatus
	SyncStatus         SyncStatus
	Version            int64
	CurrentPlanID      uuid.UUID
	CurrentPlanVersion int64
	LastConfirmedAt    *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ManagedChannel struct {
	ID                       uuid.UUID
	RelationID               uuid.UUID
	ManagedTag               string
	ExternalChannelID        *int64
	LastConfirmedPlanVersion *int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

func NewRelation(
	id uuid.UUID,
	siteID uuid.UUID,
	supplierID uuid.UUID,
	displayName string,
	saleRatio decimal.Decimal,
	visible bool,
	now time.Time,
) (Relation, error) {
	if id == uuid.Nil || siteID == uuid.Nil || supplierID == uuid.Nil {
		return Relation{}, errors.New("relation, site, and supplier IDs are required")
	}
	name, err := value.NormalizeName(displayName, 120)
	if err != nil {
		return Relation{}, fmt.Errorf("group display name: %w", err)
	}
	if !saleRatio.IsPositive() {
		return Relation{}, errors.New("sale ratio must be greater than zero")
	}
	if saleRatio.GreaterThan(decimal.RequireFromString("999999.999999")) {
		return Relation{}, errors.New("sale ratio exceeds the supported range")
	}
	if saleRatio.Exponent() < -6 {
		return Relation{}, errors.New("sale ratio supports at most 6 decimal places")
	}

	now = now.UTC()
	return Relation{
		ID:               id,
		SiteID:           siteID,
		SupplierID:       supplierID,
		GroupKey:         GroupKey(id),
		GroupDisplayName: name,
		SaleRatio:        saleRatio,
		Visible:          visible,
		DesiredStatus:    DesiredObserving,
		SyncStatus:       SyncPending,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func NewManagedChannel(id, relationID uuid.UUID, now time.Time) (ManagedChannel, error) {
	if id == uuid.Nil || relationID == uuid.Nil {
		return ManagedChannel{}, errors.New("channel and relation IDs are required")
	}
	now = now.UTC()
	return ManagedChannel{
		ID:         id,
		RelationID: relationID,
		ManagedTag: ManagedTag(relationID),
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func GroupKey(relationID uuid.UUID) string {
	return "mr_s_" + strings.ReplaceAll(relationID.String(), "-", "")
}

func ManagedTag(relationID uuid.UUID) string {
	return "manyrouter:" + relationID.String()
}
