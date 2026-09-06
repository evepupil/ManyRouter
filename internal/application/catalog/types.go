package catalog

import (
	"context"
	"time"

	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/google/uuid"
)

type TokenRecord struct {
	ID         uuid.UUID  `json:"id"`
	SiteID     uuid.UUID  `json:"site_id"`
	TokenHash  string     `json:"-"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type IssuedToken struct {
	TokenRecord
	Token string `json:"token"`
}

type Repository interface {
	CreateProductToken(context.Context, TokenRecord) error
	ListProductTokens(context.Context, uuid.UUID) ([]TokenRecord, error)
	RevokeProductToken(context.Context, uuid.UUID, uuid.UUID, string, string, time.Time) error
	AuthenticateProductToken(context.Context, string, time.Time) (uuid.UUID, error)
	LoadCatalogSource(context.Context, uuid.UUID) (domaincatalog.BuildInput, error)
	SaveProductSnapshot(context.Context, domaincatalog.Snapshot, string) (domaincatalog.Snapshot, error)
}
