package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/shopspring/decimal"
)

func (c *Client) readGroupSettings(ctx context.Context) (map[string]string, map[string]string, error) {
	var response apiResponse[[]option]
	if err := c.request(ctx, http.MethodGet, "/api/option/", nil, &response, false); err != nil {
		return nil, nil, err
	}
	var ratios map[string]string
	var userGroups map[string]string
	for _, item := range response.Data {
		switch item.Key {
		case "GroupRatio":
			parsed, err := parseGroupRatios(item.Value)
			if err != nil {
				return nil, nil, err
			}
			ratios = parsed
		case "UserUsableGroups":
			parsed := make(map[string]string)
			if err := json.Unmarshal([]byte(item.Value), &parsed); err != nil {
				return nil, nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_user_usable_groups", "New API UserUsableGroups is invalid", err)
			}
			userGroups = parsed
		}
	}
	if ratios == nil {
		return nil, nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "missing_group_ratio", "New API options did not include GroupRatio", nil)
	}
	if userGroups == nil {
		return nil, nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "missing_user_usable_groups", "New API options did not include UserUsableGroups", nil)
	}
	return ratios, userGroups, nil
}

func parseGroupRatios(value string) (map[string]string, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var raw map[string]json.Number
	if err := decoder.Decode(&raw); err != nil {
		return nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_group_ratio", "New API GroupRatio is invalid", err)
	}
	result := make(map[string]string, len(raw))
	for key, number := range raw {
		ratio, err := decimal.NewFromString(number.String())
		if err != nil || ratio.IsNegative() {
			return nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_group_ratio", fmt.Sprintf("New API group %q has an invalid ratio", key), err)
		}
		result[key] = ratio.String()
	}
	return result, nil
}

func (c *Client) readChannels(ctx context.Context) ([]reconciliation.ActualChannel, error) {
	result := make([]reconciliation.ActualChannel, 0)
	for page := 1; page <= maxChannelPages; page++ {
		path := "/api/channel/?p=" + strconv.Itoa(page) + "&page_size=100"
		var response apiResponse[channelPage]
		if err := c.request(ctx, http.MethodGet, path, nil, &response, false); err != nil {
			return nil, err
		}
		for _, item := range response.Data.Items {
			mapped, err := mapChannel(item)
			if err != nil {
				return nil, err
			}
			result = append(result, mapped)
		}
		if len(result) >= response.Data.Total || len(response.Data.Items) == 0 {
			return result, nil
		}
	}
	return nil, reconciliation.NewFailure(reconciliation.FailureCompatibility, "channel_page_limit", "New API channel list exceeded the supported page limit", nil)
}

func mapChannel(input channel) (reconciliation.ActualChannel, error) {
	mapping := make(map[string]string)
	if input.ModelMapping != nil && strings.TrimSpace(*input.ModelMapping) != "" {
		if err := json.Unmarshal([]byte(*input.ModelMapping), &mapping); err != nil {
			return reconciliation.ActualChannel{}, reconciliation.NewFailure(reconciliation.FailureCompatibility, "invalid_model_mapping", fmt.Sprintf("channel %d has invalid model mapping", input.ID), err)
		}
	}
	protocol := "unknown"
	if input.Type == channelTypeOpenAI {
		protocol = "openai_compatible"
	}
	actual := reconciliation.ActualChannel{
		ID:           input.ID,
		ManagedTag:   stringValue(input.Tag),
		Name:         input.Name,
		Protocol:     protocol,
		BaseURL:      strings.TrimRight(stringValue(input.BaseURL), "/"),
		Models:       splitCSV(input.Models),
		ModelMapping: mapping,
		Groups:       splitCSV(input.Group),
		Status:       mapStatus(input.Status),
	}
	if input.Priority != nil {
		actual.Priority = *input.Priority
	}
	if input.Weight != nil {
		actual.Weight = int(*input.Weight)
	}
	return actual, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapStatus(status int) reconciliation.ChannelStatus {
	switch status {
	case channelStatusEnabled:
		return reconciliation.ChannelEnabled
	case channelStatusManual:
		return reconciliation.ChannelManuallyDisabled
	case channelStatusAutoDisabled:
		return reconciliation.ChannelAutoDisabled
	default:
		return reconciliation.ChannelUnknown
	}
}
