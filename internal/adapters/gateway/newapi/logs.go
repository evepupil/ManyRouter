package newapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
)

const maxAdminLogPageSize = 100

// AdminLogEntry is the stable subset of a New API administrator log used by
// ManyRouter's collection boundary.
type AdminLogEntry struct {
	ID                int64  `json:"id"`
	CreatedAt         int64  `json:"created_at"`
	Type              int    `json:"type"`
	Content           string `json:"content"`
	Model             string `json:"model_name"`
	InputTokens       int64  `json:"prompt_tokens"`
	OutputTokens      int64  `json:"completion_tokens"`
	DurationSeconds   int64  `json:"use_time"`
	Stream            bool   `json:"is_stream"`
	ChannelID         int64  `json:"channel"`
	Group             string `json:"group"`
	RequestID         string `json:"request_id"`
	UpstreamRequestID string `json:"upstream_request_id"`
	Other             string `json:"other"`
}

// AdminLogPage is one page returned by New API's administrator log endpoint.
type AdminLogPage struct {
	Items    []AdminLogEntry `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// ReadAdminLogs reads one page of administrator logs using second-based time
// bounds. A log type of zero keeps New API's existing "all types" behavior.
func (c *Client) ReadAdminLogs(
	ctx context.Context,
	logType int,
	startTimestamp int64,
	endTimestamp int64,
	page int,
	pageSize int,
) (AdminLogPage, error) {
	if page < 1 {
		return AdminLogPage{}, reconciliation.NewFailure(
			reconciliation.FailureConfiguration,
			"invalid_log_page",
			"New API log page must be greater than zero",
			nil,
		)
	}
	if pageSize < 1 || pageSize > maxAdminLogPageSize {
		return AdminLogPage{}, reconciliation.NewFailure(
			reconciliation.FailureConfiguration,
			"invalid_log_page_size",
			"New API log page size must be between 1 and 100",
			nil,
		)
	}

	query := url.Values{}
	query.Set("type", strconv.Itoa(logType))
	query.Set("start_timestamp", strconv.FormatInt(startTimestamp, 10))
	query.Set("end_timestamp", strconv.FormatInt(endTimestamp, 10))
	query.Set("p", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))

	var response apiResponse[AdminLogPage]
	if err := c.request(ctx, http.MethodGet, "/api/log/?"+query.Encode(), nil, &response, false); err != nil {
		return AdminLogPage{}, err
	}
	return response.Data, nil
}
