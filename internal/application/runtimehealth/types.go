package runtimehealth

import (
	"time"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
	"github.com/google/uuid"
)

type Level string

const (
	LevelNormal    Level = "normal"
	LevelAttention Level = "attention"
	LevelBlocked   Level = "blocked"
	LevelFault     Level = "fault"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}

type JobFacts struct {
	Waiting         int64      `json:"waiting"`
	Running         int64      `json:"running"`
	Retryable       int64      `json:"retryable"`
	Failed          int64      `json:"failed"`
	OldestWaitingAt *time.Time `json:"oldest_waiting_at,omitempty"`
}

type PoolFacts struct {
	Open        int32 `json:"open"`
	InUse       int32 `json:"in_use"`
	Idle        int32 `json:"idle"`
	Max         int32 `json:"max"`
	AcquireWait int64 `json:"acquire_wait"`
}

type PeriodicFacts struct {
	CollectionAt    *time.Time `json:"collection_at,omitempty"`
	ScoringAt       *time.Time `json:"scoring_at,omitempty"`
	AutomationAt    *time.Time `json:"automation_at,omitempty"`
	CompatibilityAt *time.Time `json:"compatibility_at,omitempty"`
}

type SystemFacts struct {
	DatabaseUp              bool          `json:"database_up"`
	MigrationVersion        int64         `json:"migration_version"`
	DatabaseClockSkewSecond float64       `json:"database_clock_skew_seconds"`
	Pool                    PoolFacts     `json:"pool"`
	Jobs                    JobFacts      `json:"jobs"`
	Periodic                PeriodicFacts `json:"periodic"`
}

type SystemSnapshot struct {
	Status                      Level       `json:"status"`
	BuildVersion                string      `json:"build_version"`
	BuildCommit                 string      `json:"build_commit"`
	StartedAt                   time.Time   `json:"started_at"`
	CompatibilityCatalogVersion string      `json:"compatibility_catalog_version"`
	Facts                       SystemFacts `json:"facts"`
	Reasons                     []Reason    `json:"reasons"`
}

type RouteFacts struct {
	ConfirmedPlanID     *uuid.UUID `json:"confirmed_plan_id,omitempty"`
	ConfirmedVersion    int64      `json:"confirmed_version"`
	ConfirmedAt         *time.Time `json:"confirmed_at,omitempty"`
	LatestPlanStatus    string     `json:"latest_plan_status,omitempty"`
	LatestPlanVersion   int64      `json:"latest_plan_version"`
	LatestPlanCreatedAt *time.Time `json:"latest_plan_created_at,omitempty"`
	LastSyncStatus      string     `json:"last_sync_status,omitempty"`
	LastSyncAt          *time.Time `json:"last_sync_at,omitempty"`
	LastSyncErrorCode   string     `json:"last_sync_error_code,omitempty"`
	LastSyncError       string     `json:"last_sync_error,omitempty"`
	PendingOperations   int64      `json:"pending_operations"`
	OldestPendingAt     *time.Time `json:"oldest_pending_at,omitempty"`
}

type CollectionFacts struct {
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	DataGap             bool       `json:"data_gap"`
	SourceLatestAt      *time.Time `json:"source_latest_at,omitempty"`
}

type ScoringFacts struct {
	LastWindowAt *time.Time `json:"last_window_at,omitempty"`
	LastStatus   string     `json:"last_status,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type AutomationFacts struct {
	AutomaticStrategies int        `json:"automatic_strategies"`
	LastStatus          string     `json:"last_status,omitempty"`
	LastCompletedAt     *time.Time `json:"last_completed_at,omitempty"`
}

type ProductFacts struct {
	Version      int64      `json:"version"`
	GeneratedAt  *time.Time `json:"generated_at,omitempty"`
	FactsThrough *time.Time `json:"facts_through,omitempty"`
}

type SiteFacts struct {
	SiteID           uuid.UUID             `json:"site_id"`
	SiteCode         string                `json:"site_code"`
	SiteName         string                `json:"site_name"`
	SiteStatus       string                `json:"site_status"`
	RelationCount    int                   `json:"relation_count"`
	ProblemSuppliers int                   `json:"problem_suppliers"`
	Compatibility    *compatibility.Report `json:"compatibility,omitempty"`
	Route            RouteFacts            `json:"route"`
	Collection       CollectionFacts       `json:"collection"`
	Scoring          ScoringFacts          `json:"scoring"`
	Automation       AutomationFacts       `json:"automation"`
	Product          ProductFacts          `json:"product"`
}

type SiteSnapshot struct {
	SiteFacts
	Status  Level    `json:"status"`
	Reasons []Reason `json:"reasons"`
}

type Snapshot struct {
	Status      Level          `json:"status"`
	GeneratedAt time.Time      `json:"generated_at"`
	System      SystemSnapshot `json:"system"`
	Sites       []SiteSnapshot `json:"sites"`
}
