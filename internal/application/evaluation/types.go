package evaluation

import (
	"context"
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

var (
	ErrInvalid             = errors.New("invalid evaluation request")
	ErrDailyBudgetExceeded = errors.New("evaluation daily request budget is exhausted")
	ErrRequestKeyReused    = errors.New("evaluation request key was reused with different input")
)

const (
	FingerprintSuiteVersion = "single-token-8x12-v1"
	HealthSuiteVersion      = "stream-health-v1"
	CapabilitySuiteVersion  = "objective-capability-v1"
	AlgorithmVersion        = "m2-evaluation-v1"
)

type TargetKind string

const (
	TargetSupplierDirect TargetKind = "supplier_direct"
	TargetSiteRoute      TargetKind = "site_route"
)

type Purpose string

const (
	PurposeHealth       Purpose = "health"
	PurposeAuthenticity Purpose = "authenticity"
	PurposeQuality      Purpose = "quality"
	PurposeRecovery     Purpose = "recovery"
)

type RunStatus string

const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunUncertain RunStatus = "uncertain"
	RunCancelled RunStatus = "cancelled"
)

type Run struct {
	ID               uuid.UUID  `json:"id"`
	SupplierID       uuid.UUID  `json:"supplier_id"`
	SupplierName     string     `json:"supplier_name,omitempty"`
	RelationID       *uuid.UUID `json:"relation_id,omitempty"`
	SiteID           *uuid.UUID `json:"site_id,omitempty"`
	Model            string     `json:"model"`
	UpstreamModel    string     `json:"upstream_model"`
	TargetKind       TargetKind `json:"target_kind"`
	Purpose          Purpose    `json:"purpose"`
	Status           RunStatus  `json:"status"`
	SuiteVersion     string     `json:"suite_version"`
	AlgorithmVersion string     `json:"algorithm_version"`
	Seed             uint64     `json:"-"`
	ReferenceID      *uuid.UUID `json:"reference_id,omitempty"`
	PlannedSamples   int        `json:"planned_samples"`
	CompletedSamples int        `json:"completed_samples"`
	RequestedBy      string     `json:"requested_by"`
	RequestReason    string     `json:"request_reason"`
	ErrorCode        string     `json:"error_code,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	RequestedAt      time.Time  `json:"requested_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	NextRetryAt      *time.Time `json:"next_retry_at,omitempty"`
	RequestKey       string     `json:"-"`
	RequestHash      string     `json:"-"`
}

type TargetAccess struct {
	SupplierID    uuid.UUID
	SupplierName  string
	BaseURL       string
	Model         string
	UpstreamModel string
	Credential    credential.Record
}

type RunCommand struct {
	SupplierID  uuid.UUID  `json:"supplier_id"`
	Model       string     `json:"model"`
	Purpose     Purpose    `json:"purpose"`
	TargetKind  TargetKind `json:"target_kind"`
	Reason      string     `json:"reason"`
	Actor       string     `json:"-"`
	RequestKey  string     `json:"-"`
	RequestHash string     `json:"-"`
}

type Sample struct {
	RunID            uuid.UUID
	ProbeKey         string
	SampleIndex      int
	PromptVariant    int
	Outcome          string
	NormalizedAnswer string
	AnswerDigest     string
	ResponseModel    string
	HTTPStatus       int
	FinishReason     string
	InputTokens      int64
	OutputTokens     int64
	FirstTokenMillis *int64
	TotalMillis      *int64
	Stream           bool
	StreamCompleted  *bool
	Error            measurement.ErrorFact
	CollectedAt      time.Time
}

type ProbeRequest struct {
	Model       string
	Prompt      string
	Temperature float64
	TopP        float64
	MaxTokens   int
	Stream      bool
}

type ProbeResult struct {
	Text             string
	ResponseModel    string
	HTTPStatus       int
	FinishReason     string
	InputTokens      int64
	OutputTokens     int64
	FirstTokenMillis *int64
	TotalMillis      int64
	StreamCompleted  bool
}

type Prober interface {
	Probe(context.Context, string, []byte, ProbeRequest) (ProbeResult, error)
}

type Store interface {
	ListEvaluationTargets(context.Context) ([]TargetAccess, error)
	GetEvaluationTarget(context.Context, uuid.UUID, string) (TargetAccess, error)
	CreateEvaluationRun(context.Context, Run, int) (Run, error)
	FindEvaluationRunByRequestKey(context.Context, string) (*Run, error)
	GetEvaluationRun(context.Context, uuid.UUID) (Run, error)
	ListEvaluationRuns(context.Context, RunFilter) (RunPage, error)
	FindRecentEvaluationRun(context.Context, uuid.UUID, string, TargetKind, Purpose, time.Time) (*Run, error)
	StartEvaluationRun(context.Context, uuid.UUID, time.Time) (bool, error)
	ListEvaluationSamples(context.Context, uuid.UUID) ([]Sample, error)
	ReserveEvaluationSample(context.Context, Sample) (bool, error)
	CompleteEvaluationSample(context.Context, Sample, measurement.RequestFact, measurement.AttemptFact) error
	SaveEvaluationFingerprint(context.Context, domainevaluation.Fingerprint) error
	GetEvaluationFingerprint(context.Context, uuid.UUID) (domainevaluation.Fingerprint, error)
	FindTrustedReference(context.Context, string, time.Time) (*domainevaluation.ModelReference, error)
	GetTrustedReference(context.Context, uuid.UUID) (domainevaluation.ModelReference, error)
	FindPreviousMismatch(context.Context, domainevaluation.ModelSubject, domainevaluation.ModelReference, float64, time.Time) (*domainevaluation.MismatchEvidence, error)
	SaveAuthenticityAssessment(context.Context, uuid.UUID, domainevaluation.ModelSubject, *uuid.UUID, domainevaluation.AuthenticityAssessment, bool, time.Time) error
	SaveCapabilityAssessment(context.Context, uuid.UUID, domainevaluation.ModelSubject, float64, float64, int, int, string, time.Time) error
	CompleteEvaluationRun(context.Context, uuid.UUID, time.Time) error
	FailEvaluationRun(context.Context, uuid.UUID, RunStatus, string, string, *time.Time, time.Time) error
	CreateTrustedReference(context.Context, uuid.UUID, Run, domainevaluation.ReferenceTrust, string, string, time.Time, time.Time, string, string) (domainevaluation.ModelReference, error)
}

type Dispatcher interface {
	DispatchEvaluation(context.Context, uuid.UUID) error
}

type Vault interface {
	Decrypt(credential.Record) ([]byte, error)
}

type ReferenceCommand struct {
	RunID       uuid.UUID                       `json:"run_id"`
	Trust       domainevaluation.ReferenceTrust `json:"trust"`
	Reason      string                          `json:"reason"`
	Actor       string                          `json:"-"`
	ValidDays   int                             `json:"valid_days"`
	RequestKey  string                          `json:"-"`
	RequestHash string                          `json:"-"`
}

type RunFilter struct {
	SiteID     *uuid.UUID
	SupplierID *uuid.UUID
	Model      string
	Purpose    Purpose
	Limit      int
	Offset     int
}

type RunPage struct {
	Items  []Run `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
