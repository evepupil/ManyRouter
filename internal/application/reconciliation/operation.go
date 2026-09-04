package reconciliation

import (
	"context"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

type OperationStatus string

const (
	OperationPending         OperationStatus = "pending"
	OperationRunning         OperationStatus = "running"
	OperationUncertain       OperationStatus = "uncertain"
	OperationSucceeded       OperationStatus = "succeeded"
	OperationRetryableFailed OperationStatus = "retryable_failed"
	OperationManualRequired  OperationStatus = "manual_required"
)

type Operation struct {
	ID               uuid.UUID
	SiteID           uuid.UUID
	RelationID       uuid.UUID
	RoutePlanID      uuid.UUID
	Status           OperationStatus
	CurrentStep      *string
	Attempt          int
	LastErrorCode    *string
	LastErrorMessage *string
	NextAttemptAt    *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

type Bundle struct {
	Operation          Operation
	Site               site.Site
	Plan               routing.Plan
	ManagedChannel     routing.ManagedChannel
	AdminCredential    credential.Record
	SupplierCredential credential.Record
}

type StepStatus string

const (
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepUncertain StepStatus = "uncertain"
)

type StepRecord struct {
	OperationID  uuid.UUID
	Key          string
	Status       StepStatus
	Before       any
	After        any
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

type FailureRecord struct {
	OperationID   uuid.UUID
	SiteID        uuid.UUID
	RelationID    uuid.UUID
	RoutePlanID   uuid.UUID
	Kind          FailureKind
	Step          string
	Code          string
	Message       string
	NextAttemptAt *time.Time
	OccurredAt    time.Time
}

type SiteLock interface {
	Release(context.Context) error
}

type Store interface {
	CreateOperation(context.Context, uuid.UUID, uuid.UUID, time.Time) (Operation, error)
	GetOperation(context.Context, uuid.UUID) (Operation, error)
	LoadBundle(context.Context, uuid.UUID) (Bundle, error)
	AcquireSiteLock(context.Context, uuid.UUID) (SiteLock, bool, error)
	StartOperation(context.Context, Operation, string, time.Time) error
	RecordStep(context.Context, StepRecord) error
	BindChannel(context.Context, uuid.UUID, int64, time.Time) error
	CompleteOperation(context.Context, Bundle, int64, time.Time) error
	FailOperation(context.Context, FailureRecord) error
}

type CredentialVault interface {
	Decrypt(credential.Record) ([]byte, error)
}

type Dispatcher interface {
	Dispatch(context.Context, uuid.UUID) error
}
