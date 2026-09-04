package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/shopspring/decimal"
)

func (c *Client) SetGroupRatios(ctx context.Context, ratios map[string]string) error {
	value, err := encodeRatios(ratios)
	if err != nil {
		return err
	}
	return c.updateOption(ctx, "GroupRatio", string(value))
}

func (c *Client) SetUserUsableGroups(ctx context.Context, groups map[string]string) error {
	value, err := json.Marshal(groups)
	if err != nil {
		return reconciliation.NewFailure(reconciliation.FailureConfiguration, "encode_user_usable_groups", "could not encode user-selectable groups", err)
	}
	return c.updateOption(ctx, "UserUsableGroups", string(value))
}

func (c *Client) updateOption(ctx context.Context, key, value string) error {
	body := map[string]any{"key": key, "value": value}
	var response apiResponse[json.RawMessage]
	return c.request(ctx, http.MethodPut, "/api/option/", body, &response, true)
}

func (c *Client) CreateChannel(ctx context.Context, desired routing.DesiredChannel, upstreamKey []byte) error {
	payload, err := channelPayload(0, desired, upstreamKey, channelStatusManual)
	if err != nil {
		return err
	}
	body := map[string]any{"mode": "single", "channel": payload}
	var response apiResponse[json.RawMessage]
	return c.request(ctx, http.MethodPost, "/api/channel/", body, &response, true, string(upstreamKey))
}

func (c *Client) UpdateChannel(ctx context.Context, id int64, desired routing.DesiredChannel, upstreamKey []byte) error {
	payload, err := channelPayload(id, desired, upstreamKey, 0)
	if err != nil {
		return err
	}
	delete(payload, "status")
	var response apiResponse[json.RawMessage]
	return c.request(ctx, http.MethodPut, "/api/channel/", payload, &response, true, string(upstreamKey))
}

func (c *Client) TestChannel(ctx context.Context, id int64, model string) error {
	path := fmt.Sprintf("/api/channel/test/%d?model=%s", id, url.QueryEscape(model))
	var response apiResponse[json.RawMessage]
	return c.request(ctx, http.MethodGet, path, nil, &response, false)
}

func (c *Client) SetChannelEnabled(ctx context.Context, id int64, enabled bool) error {
	status := channelStatusManual
	if enabled {
		status = channelStatusEnabled
	}
	body := map[string]int{"status": status}
	var response apiResponse[json.RawMessage]
	return c.request(ctx, http.MethodPost, fmt.Sprintf("/api/channel/%d/status", id), body, &response, true)
}

func channelPayload(id int64, desired routing.DesiredChannel, upstreamKey []byte, status int) (map[string]any, error) {
	if desired.Protocol != "openai_compatible" {
		return nil, reconciliation.NewFailure(reconciliation.FailureConfiguration, "unsupported_protocol", "M0 only supports OpenAI-compatible suppliers", nil)
	}
	models := make([]string, 0, len(desired.Models))
	mapping := make(map[string]string)
	for _, model := range desired.Models {
		models = append(models, model.Model)
		if model.Model != model.UpstreamModel {
			mapping[model.Model] = model.UpstreamModel
		}
	}
	sort.Strings(models)
	mappingJSON := ""
	if len(mapping) > 0 {
		encoded, err := json.Marshal(mapping)
		if err != nil {
			return nil, err
		}
		mappingJSON = string(encoded)
	}
	payload := map[string]any{
		"id":            id,
		"type":          channelTypeOpenAI,
		"key":           string(upstreamKey),
		"status":        status,
		"name":          desired.Name,
		"weight":        desired.Weight,
		"base_url":      desired.BaseURL,
		"models":        strings.Join(models, ","),
		"group":         desired.GroupKey,
		"model_mapping": &mappingJSON,
		"priority":      desired.Priority,
		"auto_ban":      1,
		"tag":           desired.ManagedTag,
		"settings":      "{}",
	}
	return payload, nil
}

func encodeRatios(ratios map[string]string) ([]byte, error) {
	encoded := make(map[string]json.RawMessage, len(ratios))
	for key, raw := range ratios {
		ratio, err := decimal.NewFromString(raw)
		if err != nil || ratio.IsNegative() {
			return nil, reconciliation.NewFailure(reconciliation.FailureConfiguration, "invalid_group_ratio", fmt.Sprintf("group %q has an invalid ratio", key), err)
		}
		encoded[key] = json.RawMessage(ratio.String())
	}
	return json.Marshal(encoded)
}
