package automation

import (
	"context"
	"time"

	domainautomation "github.com/evepupil/ManyRouter/internal/domain/automation"
	"github.com/google/uuid"
)

type Mode string

const (
	ModeManual    Mode = "manual"
	ModeAutomatic Mode = "automatic"
)

type TriggerKind string

const (
	TriggerScheduled TriggerKind = "scheduled"
	TriggerOperator  TriggerKind = "operator"
)

type RunStatus string

const (
	RunFrozen      RunStatus = "frozen"
	RunPreview     RunStatus = "preview"
	RunNoChange    RunStatus = "no_change"
	RunPendingSync RunStatus = "pending_sync"
	RunSucceeded   RunStatus = "succeeded"
	RunFailed      RunStatus = "failed"
	RunUncertain   RunStatus = "uncertain"
)

type ScoreRun struct {
	ID               uuid.UUID
	SiteID           uuid.UUID
	PolicyVersion    string
	WindowEnd        time.Time
	ExpectedTargets  int
	CompletedTargets int
	Status           string
}

type Setting struct {
	StrategyID              uuid.UUID `json:"strategy_id"`
	SiteID                  uuid.UUID `json:"site_id"`
	StrategyKind            string    `json:"strategy_kind"`
	DisplayName             string    `json:"display_name"`
	Mode                    Mode      `json:"mode"`
	Version                 int64     `json:"version"`
	EntryClosedByAutomation bool      `json:"entry_closed_by_automation"`
	Reason                  string    `json:"reason"`
	UpdatedBy               string    `json:"updated_by"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type UpdateSettingCommand struct {
	SiteID       uuid.UUID
	StrategyKind string
	Mode         Mode
	Version      int64
	Reason       string
	Actor        string
}

type Candidate struct {
	RelationID    uuid.UUID
	SupplierID    uuid.UUID
	SupplierName  string
	CurrentMember bool
	Held          bool
	Models        []domainautomation.ModelAdvice
}

type StrategyInput struct {
	ID                      uuid.UUID
	Kind                    string
	DisplayName             string
	Enabled                 bool
	Visible                 bool
	Version                 int64
	Mode                    Mode
	SettingVersion          int64
	EntryClosedByAutomation bool
	CurrentMemberIDs        []uuid.UUID
	Candidates              []Candidate
}

type Input struct {
	ScoreRun   ScoreRun
	Strategies []StrategyInput
}

type Compatibility struct {
	Ready   bool
	Reasons []string
}

type Decision struct {
	ID            uuid.UUID
	StrategyID    uuid.UUID
	StrategyKind  string
	RelationID    uuid.UUID
	SupplierName  string
	Action        domainautomation.Action
	CurrentMember bool
	TargetMember  bool
	HoldAction    domainautomation.HoldAction
	Reasons       []string
	SnapshotIDs   []uuid.UUID
	CreatedAt     time.Time
}

type StrategyUpdate struct {
	StrategyID              uuid.UUID
	ExpectedStrategyVersion int64
	ExpectedSettingVersion  int64
	MemberRelationIDs       []uuid.UUID
	Visible                 bool
	EntryClosedByAutomation bool
}

type ApplyCommand struct {
	RunID       uuid.UUID
	SiteID      uuid.UUID
	ScoreRunID  uuid.UUID
	TriggerKind TriggerKind
	Status      RunStatus
	Summary     string
	StartedAt   time.Time
	CompletedAt time.Time
	Decisions   []Decision
	Strategies  []StrategyUpdate
}

type Run struct {
	ID          uuid.UUID      `json:"id"`
	SiteID      uuid.UUID      `json:"site_id"`
	ScoreRunID  uuid.UUID      `json:"score_run_id"`
	Status      RunStatus      `json:"status"`
	TriggerKind TriggerKind    `json:"trigger_kind"`
	RoutePlanID *uuid.UUID     `json:"route_plan_id,omitempty"`
	Summary     string         `json:"summary"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	Decisions   []DecisionView `json:"decisions"`
}

type DecisionView struct {
	ID            uuid.UUID `json:"id"`
	StrategyKind  string    `json:"strategy_kind"`
	RelationID    uuid.UUID `json:"relation_id"`
	SupplierName  string    `json:"supplier_name"`
	Action        string    `json:"action"`
	CurrentMember bool      `json:"current_member"`
	TargetMember  bool      `json:"target_member"`
	HoldAction    string    `json:"hold_action"`
	Reasons       []string  `json:"reasons"`
	CreatedAt     time.Time `json:"created_at"`
}

type RunFilter struct {
	SiteID *uuid.UUID
	Limit  int
	Offset int
}

type RunPage struct {
	Items  []Run `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type Repository interface {
	ListReadyScoreRuns(context.Context, int) ([]ScoreRun, error)
	GetLatestSuccessfulScoreRun(context.Context, uuid.UUID) (ScoreRun, error)
	LoadAutomationInput(context.Context, uuid.UUID) (Input, error)
	RecordAutomationRun(context.Context, ApplyCommand) (Run, error)
	ApplyAutomationRun(context.Context, ApplyCommand) (Run, error)
	ListAutomationSettings(context.Context, uuid.UUID) ([]Setting, error)
	UpdateAutomationSetting(context.Context, UpdateSettingCommand, time.Time) (Setting, error)
	ListAutomationRuns(context.Context, RunFilter) (RunPage, error)
}

type CompatibilityChecker interface {
	CheckAutomationCompatibility(context.Context, uuid.UUID) (Compatibility, error)
}
