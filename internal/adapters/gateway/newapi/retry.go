package newapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
)

func (c *Client) ReadRetryPolicy(ctx context.Context) (reconciliation.RetryPolicy, error) {
	if capabilities, err := c.ReadManagedSyncCapabilities(ctx); err == nil {
		return capabilities.RetryPolicy, nil
	}
	var response apiResponse[[]option]
	if err := c.request(ctx, http.MethodGet, "/api/option/", nil, &response, false); err != nil {
		return reconciliation.RetryPolicy{}, err
	}
	retryTimes := -1
	statusCodes := ""
	for _, item := range response.Data {
		switch item.Key {
		case "RetryTimes":
			value, err := strconv.Atoi(strings.TrimSpace(item.Value))
			if err != nil || value < 0 {
				return reconciliation.RetryPolicy{}, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_retry_times", "New API returned an invalid retry count", err)
			}
			retryTimes = value
		case "AutomaticRetryStatusCodes":
			statusCodes = item.Value
		}
	}
	if retryTimes < 0 {
		return reconciliation.RetryPolicy{}, reconciliation.NewFailure(reconciliation.FailureCompatibility, "missing_retry_times", "New API did not expose its retry count", nil)
	}
	ranges, err := parseStatusCodeRanges(statusCodes)
	if err != nil {
		return reconciliation.RetryPolicy{}, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_retry_status_codes", "New API returned invalid retry status codes", err)
	}
	return reconciliation.RetryPolicy{RetryTimes: retryTimes, StatusCodes: ranges}, nil
}

func parseStatusCodeRanges(raw string) ([]reconciliation.StatusCodeRange, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "，", ","))
	if raw == "" {
		return nil, nil
	}
	ranges := make([]reconciliation.StatusCodeRange, 0)
	for _, segment := range strings.Split(raw, ",") {
		segment = strings.ReplaceAll(strings.TrimSpace(segment), " ", "")
		if segment == "" {
			continue
		}
		parts := strings.Split(segment, "-")
		if len(parts) > 2 {
			return nil, errors.New("retry status range is invalid")
		}
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, err
		}
		end := start
		if len(parts) == 2 {
			if parts[1] == "" {
				return nil, errors.New("retry status range is invalid")
			}
			end, err = strconv.Atoi(parts[1])
			if err != nil {
				return nil, err
			}
		}
		if start < 100 || end > 599 || start > end {
			return nil, errors.New("retry status range is outside HTTP status codes")
		}
		ranges = append(ranges, reconciliation.StatusCodeRange{Start: start, End: end})
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})
	return ranges, nil
}

var _ reconciliation.RetryPolicyReader = (*Client)(nil)
