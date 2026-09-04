package supplier

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Protocol string

const ProtocolOpenAICompatible Protocol = "openai_compatible"

type Status string

const (
	StatusDraft    Status = "draft"
	StatusEnabled  Status = "enabled"
	StatusDisabled Status = "disabled"
)

type Model struct {
	Name         string
	UpstreamName string
	InputPrice   decimal.Decimal
	OutputPrice  decimal.Decimal
	Currency     string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Supplier struct {
	ID                uuid.UUID
	Code              string
	Name              string
	Protocol          Protocol
	UpstreamBaseURL   string
	CredentialID      uuid.UUID
	CredentialVersion int32
	Status            Status
	Version           int64
	Models            []Model
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ModelInput struct {
	Name         string
	UpstreamName string
	InputPrice   decimal.Decimal
	OutputPrice  decimal.Decimal
	Currency     string
}

func New(
	id uuid.UUID,
	code string,
	name string,
	baseURL string,
	credentialID uuid.UUID,
	models []ModelInput,
	now time.Time,
) (Supplier, error) {
	if id == uuid.Nil {
		return Supplier{}, errors.New("supplier ID is required")
	}
	if credentialID == uuid.Nil {
		return Supplier{}, errors.New("supplier credential ID is required")
	}

	normalizedCode, err := value.NormalizeCode(code)
	if err != nil {
		return Supplier{}, err
	}
	normalizedName, err := value.NormalizeName(name, 120)
	if err != nil {
		return Supplier{}, err
	}
	normalizedURL, err := value.NormalizeHTTPBaseURL(baseURL)
	if err != nil {
		return Supplier{}, err
	}
	normalizedModels, err := normalizeModels(models, now.UTC())
	if err != nil {
		return Supplier{}, err
	}

	now = now.UTC()
	return Supplier{
		ID:                id,
		Code:              normalizedCode,
		Name:              normalizedName,
		Protocol:          ProtocolOpenAICompatible,
		UpstreamBaseURL:   normalizedURL,
		CredentialID:      credentialID,
		CredentialVersion: 1,
		Status:            StatusEnabled,
		Version:           1,
		Models:            normalizedModels,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func normalizeModels(inputs []ModelInput, now time.Time) ([]Model, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one supplier model is required")
	}
	seen := make(map[string]struct{}, len(inputs))
	models := make([]Model, 0, len(inputs))
	maxPrice := decimal.RequireFromString("9999999999.9999999999")
	for _, input := range inputs {
		name, err := normalizeModelName(input.Name)
		if err != nil {
			return nil, fmt.Errorf("model: %w", err)
		}
		upstream, err := normalizeModelName(input.UpstreamName)
		if err != nil {
			return nil, fmt.Errorf("upstream model: %w", err)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate model %q", name)
		}
		seen[name] = struct{}{}
		if input.InputPrice.IsNegative() || input.OutputPrice.IsNegative() {
			return nil, fmt.Errorf("model %q prices must be non-negative", name)
		}
		if input.InputPrice.GreaterThan(maxPrice) || input.OutputPrice.GreaterThan(maxPrice) {
			return nil, fmt.Errorf("model %q prices exceed the supported range", name)
		}
		if input.InputPrice.Exponent() < -10 || input.OutputPrice.Exponent() < -10 {
			return nil, fmt.Errorf("model %q prices support at most 10 decimal places", name)
		}
		currency := strings.ToUpper(strings.TrimSpace(input.Currency))
		if len(currency) != 3 {
			return nil, fmt.Errorf("model %q currency must contain three letters", name)
		}
		for _, char := range currency {
			if char < 'A' || char > 'Z' {
				return nil, fmt.Errorf("model %q currency must contain three letters", name)
			}
		}
		models = append(models, Model{
			Name:         name,
			UpstreamName: upstream,
			InputPrice:   input.InputPrice,
			OutputPrice:  input.OutputPrice,
			Currency:     currency,
			Enabled:      true,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func normalizeModelName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len(name) > 191 {
		return "", errors.New("name must not exceed 191 bytes")
	}
	if strings.ContainsAny(name, ",\r\n") {
		return "", errors.New("name must not contain commas or line breaks")
	}
	return name, nil
}

func (s Supplier) CanDeploy() error {
	if s.Status != StatusEnabled {
		return errors.New("supplier is not enabled")
	}
	for _, model := range s.Models {
		if model.Enabled {
			return nil
		}
	}
	return errors.New("supplier has no enabled models")
}
