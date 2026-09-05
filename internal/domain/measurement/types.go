package measurement

import (
	"time"

	"github.com/google/uuid"
)

const (
	MeasurementRuleVersion         = "measurement-v1"
	ErrorClassificationRuleVersion = "error-classification-v2"
	DurationResolutionMillisecond  = int64(1)
	DurationResolutionSecond       = int64(1000)
)

type Source string

const (
	SourceRealTraffic Source = "real_traffic"
	SourceDirectProbe Source = "direct_probe"
	SourceSiteProbe   Source = "site_probe"
)

type FinalResult string

const (
	FinalSucceeded  FinalResult = "succeeded"
	FinalFailed     FinalResult = "failed"
	FinalCancelled  FinalResult = "cancelled"
	FinalRejected   FinalResult = "rejected"
	FinalIncomplete FinalResult = "incomplete"
)

type AttemptResult string

const (
	AttemptSucceeded  AttemptResult = "succeeded"
	AttemptFailed     AttemptResult = "failed"
	AttemptCancelled  AttemptResult = "cancelled"
	AttemptRejected   AttemptResult = "rejected"
	AttemptIncomplete AttemptResult = "incomplete"
)

type StreamCompletion string

const (
	StreamUnknown    StreamCompletion = "unknown"
	StreamCompleted  StreamCompletion = "completed"
	StreamIncomplete StreamCompletion = "incomplete"
)

type ErrorClass string

const (
	ErrorNone                ErrorClass = "none"
	ErrorRateLimited         ErrorClass = "rate_limited"
	ErrorAuthentication      ErrorClass = "authentication"
	ErrorBalanceExhausted    ErrorClass = "balance_exhausted"
	ErrorTimeout             ErrorClass = "timeout"
	ErrorInvalidRequest      ErrorClass = "invalid_request"
	ErrorUpstreamUnavailable ErrorClass = "upstream_unavailable"
	ErrorStreamIncomplete    ErrorClass = "stream_incomplete"
	ErrorCancelled           ErrorClass = "cancelled"
	ErrorRejected            ErrorClass = "rejected"
	ErrorUnknown             ErrorClass = "unknown"
)

type ErrorResponsibility string

const (
	ResponsibilityUser     ErrorResponsibility = "user"
	ResponsibilitySupplier ErrorResponsibility = "supplier"
	ResponsibilitySite     ErrorResponsibility = "site"
	ResponsibilityUnknown  ErrorResponsibility = "unknown"
)

type AttributionStatus string

const (
	AttributionMapped  AttributionStatus = "mapped"
	AttributionPending AttributionStatus = "pending"
)

type Attribution struct {
	Status     AttributionStatus
	RelationID uuid.UUID
	SupplierID uuid.UUID
}

type ErrorFact struct {
	Class          ErrorClass
	Responsibility ErrorResponsibility
	StableCode     string
	Summary        string
	RuleVersion    string
}

type Cursor struct {
	OccurredAt time.Time
	SourceID   string
}

func (cursor Cursor) IsZero() bool {
	return cursor.OccurredAt.IsZero() && cursor.SourceID == ""
}

func (cursor Cursor) Compare(other Cursor) int {
	if cursor.OccurredAt.Before(other.OccurredAt) {
		return -1
	}
	if cursor.OccurredAt.After(other.OccurredAt) {
		return 1
	}
	if cursor.SourceID < other.SourceID {
		return -1
	}
	if cursor.SourceID > other.SourceID {
		return 1
	}
	return 0
}

type RequestFact struct {
	SourceHash               string
	RequestHash              string
	RuleVersion              string
	Source                   Source
	SiteID                   uuid.UUID
	RequestID                string
	TerminalCursor           Cursor
	Model                    string
	Group                    string
	Result                   FinalResult
	HTTPStatus               int
	IsStream                 bool
	StreamCompletion         StreamCompletion
	FinalChannelID           int64
	Attribution              Attribution
	FirstTokenMillis         *int64
	TotalMillis              *int64
	DurationResolutionMillis int64
	PromptTokens             int64
	CompletionTokens         int64
	TotalTokens              int64
	AttemptCount             int
	OccurredAt               time.Time
	Error                    ErrorFact
}

type AttemptFact struct {
	SourceHash               string
	RequestHash              string
	RuleVersion              string
	Source                   Source
	SiteID                   uuid.UUID
	RequestID                string
	Ordinal                  int
	ChannelID                int64
	Model                    string
	Attribution              Attribution
	Result                   AttemptResult
	HTTPStatus               int
	IsFinal                  bool
	IsStream                 bool
	ProducedVisibleOutput    bool
	StreamCompletion         StreamCompletion
	FirstTokenMillis         *int64
	TotalMillis              *int64
	DurationResolutionMillis int64
	OccurredAt               time.Time
	Error                    ErrorFact
}

type QuarantineFact struct {
	SourceHash string
	Source     Source
	SiteID     uuid.UUID
	Cursor     Cursor
	ReasonCode string
}

type Batch struct {
	Source      Source
	SiteID      uuid.UUID
	From        Cursor
	Next        Cursor
	Requests    []RequestFact
	Attempts    []AttemptFact
	Quarantines []QuarantineFact
}
