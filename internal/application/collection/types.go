package collection

import (
	"context"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	"github.com/google/uuid"
)

const (
	SourceContractVersion = "new_api_http_v1"
	newAPIConsumeLogType  = 2
	newAPIErrorLogType    = 5
)

type SiteAccess struct {
	ID          uuid.UUID
	Code        string
	Name        string
	BaseURL     string
	AdminUserID int64
	Credential  credential.Record
}

type CursorState struct {
	Cursor             measurement.Cursor
	ScannedThrough     time.Time
	Overlap            time.Duration
	SourceLatest       time.Time
	LastReadAt         time.Time
	LastSuccessAt      time.Time
	LastErrorAt        time.Time
	LastErrorCode      string
	LastErrorMessage   string
	ConsecutiveFailure int
	DataGap            bool
}

type ChannelBinding struct {
	ChannelID  int64
	RelationID uuid.UUID
	SupplierID uuid.UUID
	ValidFrom  time.Time
	ValidTo    *time.Time
}

func (binding ChannelBinding) ActiveAt(at time.Time) bool {
	if at.Before(binding.ValidFrom) {
		return false
	}
	return binding.ValidTo == nil || at.Before(*binding.ValidTo)
}

type RemoteLog struct {
	ID                int64
	CreatedAt         int64
	Type              int
	Model             string
	InputTokens       int64
	OutputTokens      int64
	DurationSeconds   int64
	Stream            bool
	ChannelID         int64
	Group             string
	RequestID         string
	UpstreamRequestID string
	ErrorText         string
	Other             string
}

type RemotePage struct {
	Items    []RemoteLog
	Total    int64
	Page     int
	PageSize int
}

type LogReader interface {
	Read(context.Context, int, int64, int64, int, int) (RemotePage, error)
}

type LogReaderFactory interface {
	NewLogReader(string, []byte, int64) (LogReader, error)
}

type Store interface {
	ListCollectionSites(context.Context) ([]SiteAccess, error)
	GetCollectionSite(context.Context, uuid.UUID) (SiteAccess, error)
	GetCollectionCursor(context.Context, uuid.UUID) (CursorState, error)
	ListChannelBindings(context.Context, uuid.UUID, time.Time, time.Time) ([]ChannelBinding, error)
	SaveMeasurementBatch(context.Context, measurement.Batch, time.Time, bool, time.Time, time.Time, time.Time) (int, int, error)
	MarkCollectionFailure(context.Context, uuid.UUID, string, string, time.Time) error
	ListCollectionStatus(context.Context, *uuid.UUID) ([]Status, error)
}

type Vault interface {
	Decrypt(credential.Record) ([]byte, error)
}

type Result struct {
	SiteID         uuid.UUID  `json:"site_id"`
	SiteName       string     `json:"site_name"`
	ReadRecords    int        `json:"read_records"`
	SavedRequests  int        `json:"saved_requests"`
	SavedAttempts  int        `json:"saved_attempts"`
	CursorTime     *time.Time `json:"cursor_time,omitempty"`
	ScannedThrough *time.Time `json:"scanned_through,omitempty"`
	SourceLatest   *time.Time `json:"source_latest,omitempty"`
	DataGap        bool       `json:"data_gap"`
	ErrorCode      string     `json:"error_code,omitempty"`
}

type SweepResult struct {
	Sites []Result `json:"sites"`
}

type Status struct {
	SiteID             uuid.UUID  `json:"site_id"`
	SiteName           string     `json:"site_name"`
	SourceKind         string     `json:"source_kind"`
	ContractVersion    string     `json:"contract_version"`
	CursorTime         *time.Time `json:"cursor_time,omitempty"`
	ScannedThrough     *time.Time `json:"scanned_through,omitempty"`
	SourceLatest       *time.Time `json:"source_latest,omitempty"`
	LastReadAt         *time.Time `json:"last_read_at,omitempty"`
	LastSuccessAt      *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
	LastErrorCode      string     `json:"last_error_code,omitempty"`
	LastErrorMessage   string     `json:"last_error_message,omitempty"`
	ConsecutiveFailure int        `json:"consecutive_failures"`
	DataGap            bool       `json:"data_gap"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
}
