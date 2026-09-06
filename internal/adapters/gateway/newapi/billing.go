package newapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/shopspring/decimal"
)

// Only pricing settings enter durable price evidence; unrelated options may contain secrets.
var billingOptionKeys = map[string]bool{
	"ModelRatio": true, "CompletionRatio": true, "ModelPrice": true,
	"CacheRatio": true, "CreateCacheRatio": true, "CreateCacheRatio5m": true,
	"CreateCacheRatio1h": true, "CacheCreationRatio": true, "ImageRatio": true,
	"AudioRatio": true, "AudioCompletionRatio": true, "GroupGroupRatio": true,
	"QuotaPerUnit": true, "USDExchangeRate": true, "UnitPrice": true,
}

func (c *Client) ReadBillingBasis(ctx context.Context) (map[string]json.RawMessage, string, error) {
	if capabilities, err := c.ReadManagedSyncCapabilities(ctx); err == nil {
		return capabilities.BillingBasis, capabilities.BillingBasisHash, nil
	}
	var response apiResponse[[]option]
	if err := c.request(ctx, http.MethodGet, "/api/option/", nil, &response, false); err != nil {
		return nil, "", err
	}
	basis := make(map[string]json.RawMessage)
	version, err := c.Probe(ctx)
	if err != nil {
		return nil, "", err
	}
	encodedVersion, err := json.Marshal(version)
	if err != nil {
		return nil, "", err
	}
	basis["NewAPIVersion"] = encodedVersion
	for _, item := range response.Data {
		if !billingOptionKeys[item.Key] {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(item.Value))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, "", reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_billing_basis", "New API returned an invalid pricing setting", err)
		}
		value, err := normalizeBillingValue(value)
		if err != nil {
			return nil, "", err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, "", err
		}
		basis[item.Key] = encoded
	}
	for _, required := range []string{"ModelRatio", "CompletionRatio", "ModelPrice"} {
		if _, ok := basis[required]; !ok {
			return nil, "", reconciliation.NewFailure(reconciliation.FailureCompatibility, "billing_basis_missing", "New API did not expose required pricing settings", nil)
		}
	}
	encoded, err := json.Marshal(basis)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return basis, hex.EncodeToString(digest[:]), nil
}

func normalizeBillingValue(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		number, err := decimal.NewFromString(string(typed))
		if err != nil {
			return nil, err
		}
		return json.Number(number.String()), nil
	case map[string]any:
		for key, item := range typed {
			normalized, err := normalizeBillingValue(item)
			if err != nil {
				return nil, err
			}
			typed[key] = normalized
		}
	case []any:
		for index, item := range typed {
			normalized, err := normalizeBillingValue(item)
			if err != nil {
				return nil, err
			}
			typed[index] = normalized
		}
	}
	return value, nil
}
