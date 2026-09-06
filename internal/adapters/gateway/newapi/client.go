package newapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/value"
)

const (
	channelTypeOpenAI         = 1
	channelStatusEnabled      = 1
	channelStatusManual       = 2
	channelStatusAutoDisabled = 3
	maxResponseBytes          = 2 << 20
	maxChannelPages           = 100
)

type Factory struct {
	HTTPClient *http.Client
}

func (f Factory) New(baseURL string, accessToken []byte) (reconciliation.Gateway, error) {
	client := f.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return NewClient(baseURL, accessToken, client)
}

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
	adminUserID int64
}

func NewClient(baseURL string, accessToken []byte, httpClient *http.Client) (*Client, error) {
	normalizedURL, err := value.NormalizeHTTPBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if len(accessToken) == 0 {
		return nil, errors.New("New API access token is required")
	}
	if httpClient == nil {
		return nil, errors.New("HTTP client is required")
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: normalizedURL, accessToken: string(accessToken), httpClient: &clientCopy, adminUserID: 1}, nil
}

func (f Factory) NewForSite(baseURL string, accessToken []byte, adminUserID int64) (reconciliation.Gateway, error) {
	gateway, err := f.New(baseURL, accessToken)
	if err != nil {
		return nil, err
	}
	client, ok := gateway.(*Client)
	if !ok || adminUserID < 1 {
		return nil, errors.New("valid New API management user ID is required")
	}
	client.adminUserID = adminUserID
	return client, nil
}

type apiResponse[T any] struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type statusData struct {
	Version string `json:"version"`
}

type option struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type channel struct {
	ID           int64   `json:"id"`
	Type         int     `json:"type"`
	Status       int     `json:"status"`
	Name         string  `json:"name"`
	Weight       *uint   `json:"weight"`
	BaseURL      *string `json:"base_url"`
	Models       string  `json:"models"`
	Group        string  `json:"group"`
	ModelMapping *string `json:"model_mapping"`
	Priority     *int64  `json:"priority"`
	Tag          *string `json:"tag"`
}

type channelPage struct {
	Items    []channel `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

func (c *Client) Probe(ctx context.Context) (string, error) {
	var response apiResponse[statusData]
	if err := c.request(ctx, http.MethodGet, "/api/status", nil, &response, false); err != nil {
		return "", err
	}
	if response.Data.Version == "" {
		return "", reconciliation.NewFailure(reconciliation.FailureCompatibility, "missing_version", "New API status did not include a version", nil)
	}
	return response.Data.Version, nil
}

func (c *Client) ReadActualState(ctx context.Context) (reconciliation.ActualState, error) {
	if managed, err := c.ReadManagedState(ctx); err == nil {
		return managed.Actual, nil
	}
	version, err := c.Probe(ctx)
	if err != nil {
		return reconciliation.ActualState{}, err
	}
	ratios, userGroups, err := c.readGroupSettings(ctx)
	if err != nil {
		return reconciliation.ActualState{}, err
	}
	channels, err := c.readChannels(ctx)
	if err != nil {
		return reconciliation.ActualState{}, err
	}
	return reconciliation.ActualState{
		Version: version, GroupRatios: ratios, UserUsableGroups: userGroups, Channels: channels,
	}, nil
}
