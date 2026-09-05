package scoring

import (
	"context"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Target struct {
	SiteID            uuid.UUID
	RelationID        uuid.UUID
	SupplierID        uuid.UUID
	SupplierName      string
	Model             string
	InputPrice        decimal.Decimal
	OutputPrice       decimal.Decimal
	Currency          string
	DesiredStatus     string
	SyncStatus        string
	CurrentStrategies []domainscoring.AutoKind
}

type WindowMetrics struct {
	AttemptCount         uint64
	SLAAttemptCount      uint64
	SuccessCount         uint64
	FailureCount         uint64
	SLAFailureCount      uint64
	RateLimitedCount     uint64
	AuthenticationCount  uint64
	BalanceCount         uint64
	TimeoutCount         uint64
	TransportCount       uint64
	UpstreamCount        uint64
	StreamCount          uint64
	StreamCompletedCount uint64
	TTFTCount            uint64
	SuccessDurationCount uint64
	FailureDurationCount uint64
	CoarseDurationCount  uint64
	RecoveryMillis       uint64
	TTFT                 domainscoring.LatencyHistogram
	SuccessDuration      domainscoring.LatencyHistogram
	FailureDuration      domainscoring.LatencyHistogram
	FactsThrough         time.Time
	PendingAttribution   bool
}

type CollectionEvidence struct {
	LastSuccessAt time.Time
	SourceLatest  time.Time
	DataGap       bool
}

type PriceEvidence struct {
	Available            bool    `json:"available"`
	ChangesPerDay        uint64  `json:"changes_per_day"`
	ChangeMagnitudeRatio float64 `json:"change_magnitude_ratio"`
}

type FailureStreak struct {
	Total          uint64 `json:"total"`
	Authentication uint64 `json:"authentication"`
	Balance        uint64 `json:"balance"`
}

type EvaluationEvidence struct {
	AuthenticityID         *uuid.UUID
	Authenticity           domainevaluation.Verdict
	AuthenticityConfidence float64
	AuthenticityCheckedAt  time.Time
	CapabilityID           *uuid.UUID
	CapabilityScore        float64
	CapabilityConfidence   float64
	CapabilityCheckedAt    time.Time
	CapabilityChecks       int
	HealthScore            float64
	HealthConfidence       float64
	HealthCheckedAt        time.Time
}

type Snapshot struct {
	ID                       uuid.UUID
	Target                   Target
	WindowStart              time.Time
	WindowEnd                time.Time
	FactsThrough             *time.Time
	PassiveSamples           uint64
	ActiveSamples            uint64
	Scores                   *domainscoring.DimensionScores
	BalancedScore            *domainscoring.Score
	Confidence               domainscoring.Confidence
	Eligibility              string
	HardReasons              []domainscoring.GateReason
	Explanation              any
	AuthenticityAssessmentID *uuid.UUID
	CapabilityAssessmentID   *uuid.UUID
	CreatedAt                time.Time
	Recommendations          []domainscoring.ShadowAdvice
}

type Repository interface {
	RefreshMinuteMetrics(context.Context, time.Time, time.Time, time.Time, time.Time) error
	ListScoringTargets(context.Context) ([]Target, error)
	GetLowestPeerCost(context.Context, uuid.UUID, string, string) (decimal.Decimal, error)
	GetPriceEvidence(context.Context, Target, time.Time, time.Time) (PriceEvidence, error)
	GetWindowMetrics(context.Context, Target, time.Time, time.Time) (WindowMetrics, error)
	GetCollectionEvidence(context.Context, uuid.UUID) (CollectionEvidence, error)
	GetEvaluationEvidence(context.Context, uuid.UUID, string, time.Time) (EvaluationEvidence, error)
	GetFailureStreak(context.Context, Target, time.Time) (FailureStreak, error)
	FindPreviousRecommendation(context.Context, Target, domainscoring.AutoKind, time.Time) (*PreviousRecommendation, error)
	SaveScoreSnapshot(context.Context, Snapshot) error
	ListInsights(context.Context, InsightFilter) (InsightPage, error)
}

type InsightFilter struct {
	SiteID     *uuid.UUID
	SupplierID *uuid.UUID
	Model      string
	Limit      int
	Offset     int
}

type PreviousRecommendation struct {
	Score      *domainscoring.Score
	CreatedAt  time.Time
	Confidence domainscoring.Confidence
}

type InsightRecommendation struct {
	StrategyKind  string   `json:"strategy_kind"`
	Action        string   `json:"action"`
	CurrentMember bool     `json:"current_member"`
	Score         *float64 `json:"score,omitempty"`
	Confidence    string   `json:"confidence"`
	Reasons       []string `json:"reasons"`
}

type Insight struct {
	SnapshotID          uuid.UUID               `json:"snapshot_id"`
	PolicyVersion       string                  `json:"policy_version"`
	SiteID              uuid.UUID               `json:"site_id"`
	SupplierID          uuid.UUID               `json:"supplier_id"`
	SupplierName        string                  `json:"supplier_name"`
	Model               string                  `json:"model"`
	PassiveSamples      int64                   `json:"passive_samples"`
	ActiveSamples       int64                   `json:"active_samples"`
	PriceScore          *float64                `json:"price_score,omitempty"`
	LatencyScore        *float64                `json:"latency_score,omitempty"`
	SLAScore            *float64                `json:"sla_score,omitempty"`
	QualityScore        *float64                `json:"quality_score,omitempty"`
	TotalScore          *float64                `json:"total_score,omitempty"`
	Confidence          string                  `json:"confidence"`
	Eligibility         string                  `json:"eligibility"`
	HardReasons         []string                `json:"hard_reasons"`
	AuthenticityVerdict string                  `json:"authenticity_verdict"`
	FactsThrough        *time.Time              `json:"facts_through,omitempty"`
	WindowStart         time.Time               `json:"window_start"`
	WindowEnd           time.Time               `json:"window_end"`
	CreatedAt           time.Time               `json:"created_at"`
	Explanation         map[string]any          `json:"explanation"`
	Recommendations     []InsightRecommendation `json:"recommendations"`
}

type InsightPage struct {
	Items  []Insight `json:"items"`
	Total  int64     `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}
