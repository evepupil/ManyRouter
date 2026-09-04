package site

import (
	"errors"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
)

type Status string

const (
	StatusEnabled  Status = "enabled"
	StatusDisabled Status = "disabled"
)

type CompatibilityStatus string

const (
	CompatibilityUnknown      CompatibilityStatus = "unknown"
	CompatibilityCompatible   CompatibilityStatus = "compatible"
	CompatibilityIncompatible CompatibilityStatus = "incompatible"
)

type Site struct {
	ID                  uuid.UUID
	Code                string
	Name                string
	NewAPIBaseURL       string
	AdminCredentialID   uuid.UUID
	Status              Status
	CompatibilityStatus CompatibilityStatus
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func New(id uuid.UUID, code, name, baseURL string, credentialID uuid.UUID, now time.Time) (Site, error) {
	if id == uuid.Nil {
		return Site{}, errors.New("site ID is required")
	}
	if credentialID == uuid.Nil {
		return Site{}, errors.New("site credential ID is required")
	}

	normalizedCode, err := value.NormalizeCode(code)
	if err != nil {
		return Site{}, err
	}
	normalizedName, err := value.NormalizeName(name, 120)
	if err != nil {
		return Site{}, err
	}
	normalizedURL, err := value.NormalizeHTTPBaseURL(baseURL)
	if err != nil {
		return Site{}, err
	}

	now = now.UTC()
	return Site{
		ID:                  id,
		Code:                normalizedCode,
		Name:                normalizedName,
		NewAPIBaseURL:       normalizedURL,
		AdminCredentialID:   credentialID,
		Status:              StatusEnabled,
		CompatibilityStatus: CompatibilityUnknown,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil
}

func (s Site) CanSync() error {
	if s.Status != StatusEnabled {
		return errors.New("site is disabled")
	}
	if s.CompatibilityStatus == CompatibilityIncompatible {
		return errors.New("site is incompatible")
	}
	return nil
}
