package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
)

func (c *Client) request(ctx context.Context, method, path string, body any, target any, write bool, redactions ...string) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return reconciliation.NewFailure(reconciliation.FailureConfiguration, "encode_request", "could not encode New API request", err)
		}
		defer clear(encoded)
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return reconciliation.NewFailure(reconciliation.FailureConfiguration, "create_request", "could not create New API request", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		kind := reconciliation.FailureRetryable
		code := "gateway_unavailable"
		if write {
			kind = reconciliation.FailureUncertain
			code = "write_result_unknown"
		}
		return reconciliation.NewFailure(kind, code, "New API request failed", err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return reconciliation.NewFailure(reconciliation.FailureRetryable, "read_response", "could not read New API response", err)
	}
	if len(payload) > maxResponseBytes {
		return reconciliation.NewFailure(reconciliation.FailureCompatibility, "response_too_large", "New API response exceeded the size limit", nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		kind := reconciliation.FailureRetryable
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			kind = reconciliation.FailureAuthentication
		} else if response.StatusCode >= 300 && response.StatusCode < 500 {
			kind = reconciliation.FailureCompatibility
		}
		return reconciliation.NewFailure(kind, "gateway_http_"+strconv.Itoa(response.StatusCode), fmt.Sprintf("New API returned HTTP %d", response.StatusCode), nil)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return reconciliation.NewFailure(reconciliation.FailureCompatibility, "decode_response", "New API returned invalid JSON", err)
	}
	success, message, ok := responseStatus(target)
	if !ok {
		return reconciliation.NewFailure(reconciliation.FailureCompatibility, "missing_success", "New API response did not include success", nil)
	}
	if !success {
		allRedactions := append([]string{c.accessToken}, redactions...)
		return reconciliation.NewFailure(reconciliation.FailureConfiguration, "gateway_business_error", sanitize(message, allRedactions), nil)
	}
	return nil
}

func responseStatus(target any) (bool, string, bool) {
	payload, err := json.Marshal(target)
	if err != nil {
		return false, "", false
	}
	var envelope struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Success == nil {
		return false, "", false
	}
	return *envelope.Success, envelope.Message, true
}

func sanitize(message string, secrets []string) string {
	clean := strings.TrimSpace(message)
	for _, secret := range secrets {
		if secret != "" {
			clean = strings.ReplaceAll(clean, secret, "[redacted]")
		}
	}
	if clean == "" {
		clean = "New API rejected the request"
	}
	if len(clean) > 512 {
		clean = clean[:512]
	}
	return clean
}
