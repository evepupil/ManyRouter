package routing

import (
	"time"

	"github.com/google/uuid"
)

type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanApplying   PlanStatus = "applying"
	PlanConfirmed  PlanStatus = "confirmed"
	PlanFailed     PlanStatus = "failed"
	PlanUncertain  PlanStatus = "uncertain"
	PlanSuperseded PlanStatus = "superseded"
)

type Plan struct {
	ID             uuid.UUID
	SiteID         uuid.UUID
	RelationID     uuid.UUID
	Version        int64
	PreviousPlanID *uuid.UUID
	Reason         string
	Snapshot       Snapshot
	SnapshotJSON   []byte
	ContentHash    string
	Status         PlanStatus
	CreatedAt      time.Time
	ConfirmedAt    *time.Time
}
