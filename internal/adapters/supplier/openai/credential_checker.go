package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxModelsResponseBytes = 1 << 20

type CredentialChecker struct {
	client *http.Client
}

func NewCredentialChecker(client *http.Client) (*CredentialChecker, error) {
	if client == nil {
		return nil, errors.New("supplier credential HTTP client is required")
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CredentialChecker{client: &copy}, nil
}

func (c *CredentialChecker) Check(ctx context.Context, baseURL string, key []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return errors.New("create supplier credential check request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(key))
	response, err := c.client.Do(request)
	if err != nil {
		return errors.New("supplier credential check request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("supplier credential was rejected")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxModelsResponseBytes+1))
	if err != nil || len(payload) > maxModelsResponseBytes {
		return errors.New("supplier model response exceeded the supported size")
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &models); err != nil || len(models.Data) == 0 {
		return errors.New("supplier returned no available models")
	}
	return nil
}
