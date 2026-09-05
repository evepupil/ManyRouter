package operations

import (
	"encoding/json"
	"errors"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/google/uuid"
)

var (
	ErrInvalid  = errors.New("invalid operation")
	ErrConflict = errors.New("configuration version changed")
	ErrBusy     = errors.New("configuration is busy")
	ErrNotFound = errors.New("business object not found")
)

type Filter struct {
	Query      string
	SiteID     uuid.UUID
	SupplierID uuid.UUID
	Limit      int
	Offset     int
}

type Page struct {
	Items  []json.RawMessage `json:"items"`
	Total  int64             `json:"total"`
	Offset int               `json:"offset"`
	Limit  int               `json:"limit"`
}

type ModelInput struct {
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
	InputPrice    string `json:"input_price"`
	OutputPrice   string `json:"output_price"`
	Currency      string `json:"currency"`
	Enabled       bool   `json:"enabled"`
}

type SiteInput struct {
	Code          string `json:"code,omitempty"`
	Name          string `json:"name"`
	NewAPIBaseURL string `json:"new_api_base_url"`
	AccessToken   string `json:"new_api_access_token,omitempty"`
	AdminUserID   int64  `json:"admin_user_id"`
	Status        string `json:"status,omitempty"`
	Version       int64  `json:"version,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type SupplierInput struct {
	Code    string       `json:"code,omitempty"`
	Name    string       `json:"name"`
	BaseURL string       `json:"upstream_base_url"`
	APIKey  string       `json:"upstream_api_key,omitempty"`
	Models  []ModelInput `json:"models"`
	Status  string       `json:"status,omitempty"`
	Version int64        `json:"version,omitempty"`
	Reason  string       `json:"reason,omitempty"`
}

type CredentialInput struct {
	Version int64  `json:"version"`
	APIKey  string `json:"api_key"`
	Reason  string `json:"reason"`
}

type CredentialCancelInput struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

type DeploymentTarget struct {
	SiteID      uuid.UUID `json:"site_id"`
	DisplayName string    `json:"group_display_name"`
	SaleRatio   string    `json:"sale_ratio"`
	Visible     bool      `json:"visible"`
}

type DeploymentInput struct {
	SupplierID uuid.UUID          `json:"supplier_id"`
	Sites      []DeploymentTarget `json:"sites"`
	Reason     string             `json:"reason"`
}

type RelationInput struct {
	Version       int64  `json:"version"`
	DisplayName   string `json:"group_display_name"`
	Visible       bool   `json:"visible"`
	DesiredStatus string `json:"desired_status"`
	Resume        bool   `json:"resume"`
	Reason        string `json:"reason"`
}

type StrategyInput struct {
	Version           int64       `json:"version"`
	Enabled           bool        `json:"enabled"`
	Visible           bool        `json:"visible"`
	DisplayName       string      `json:"display_name"`
	MemberRelationIDs []uuid.UUID `json:"member_relation_ids"`
	Reason            string      `json:"reason"`
}

type PriceInput struct {
	SiteID    uuid.UUID `json:"site_id"`
	GroupKey  string    `json:"group_key"`
	SaleRatio string    `json:"sale_ratio"`
	Reason    string    `json:"reason"`
}

type PublishInput struct {
	Version int64 `json:"version"`
}
type RestoreInput struct {
	Reason string `json:"reason"`
}

type BillingBasis struct {
	Values map[string]json.RawMessage `json:"values"`
	Hash   string                     `json:"hash"`
}

type Mutation struct {
	Kind         string
	ID           uuid.UUID
	StrategyKind string
	Actor        string
	Key          string
	RequestHash  string
	Input        any
	Sealed       *credential.Record
	Bases        map[uuid.UUID]BillingBasis
}

type SiteAccess struct {
	BaseURL     string
	AdminUserID int64
	Credential  credential.Record
}

type SupplierAccess struct {
	BaseURL    string
	TestModel  string
	Version    int64
	Credential credential.Record
}
