package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

type managedSyncCapabilities struct {
	ContractVersion  string                     `json:"contract_version"`
	NewAPIVersion    string                     `json:"new_api_version"`
	DatabaseType     string                     `json:"database_type"`
	BillingBasis     map[string]json.RawMessage `json:"billing_basis"`
	BillingBasisHash string                     `json:"billing_basis_hash"`
	Features         managedSyncFeatures        `json:"features"`
	Limits           managedSyncLimits          `json:"limits"`
	RetryPolicy      managedSyncRetryPolicy     `json:"retry_policy"`
}

type managedSyncFeatures struct {
	AtomicApply           bool `json:"atomic_apply"`
	ManagedChannels       bool `json:"managed_channels"`
	MultipleGroups        bool `json:"multiple_groups"`
	GroupRatios           bool `json:"group_ratios"`
	EntryVisibility       bool `json:"entry_visibility"`
	PersistentIdempotency bool `json:"persistent_idempotency"`
	FinalStateDigest      bool `json:"final_state_digest"`
	LogRead               bool `json:"log_read"`
}

type managedSyncLimits struct {
	MaxChannels      int   `json:"max_channels"`
	MaxGroups        int   `json:"max_groups"`
	MaxModels        int   `json:"max_models"`
	MaxGroupKeyBytes int   `json:"max_group_key_bytes"`
	MaxRequestBytes  int64 `json:"max_request_bytes"`
}

type managedSyncRetryPolicy struct {
	RetryTimes  int    `json:"retry_times"`
	StatusCodes string `json:"status_codes"`
}

type managedSyncModel struct {
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
}

type managedSyncDesiredChannel struct {
	ManagedTag        string             `json:"managed_tag"`
	Name              string             `json:"name"`
	BaseURL           string             `json:"base_url"`
	APIKey            string             `json:"api_key"`
	CredentialVersion int32              `json:"credential_version"`
	Models            []managedSyncModel `json:"models"`
	Groups            []string           `json:"groups"`
	Priority          int64              `json:"priority"`
	Weight            int                `json:"weight"`
	DesiredStatus     string             `json:"desired_status"`
	Resume            bool               `json:"resume"`
}

type managedSyncChannel struct {
	ID                int64              `json:"id"`
	ManagedTag        string             `json:"managed_tag"`
	Name              string             `json:"name"`
	BaseURL           string             `json:"base_url"`
	CredentialVersion int32              `json:"credential_version"`
	Models            []managedSyncModel `json:"models"`
	Groups            []string           `json:"groups"`
	Priority          int64              `json:"priority"`
	Weight            int                `json:"weight"`
	Status            string             `json:"status"`
}

type managedSyncGroup struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	SaleRatio   string `json:"sale_ratio"`
	Visible     bool   `json:"visible"`
}

type managedSyncState struct {
	ContractVersion  string               `json:"contract_version"`
	NewAPIVersion    string               `json:"new_api_version"`
	StateHash        string               `json:"state_hash"`
	BillingBasisHash string               `json:"billing_basis_hash"`
	Channels         []managedSyncChannel `json:"channels"`
	Groups           []managedSyncGroup   `json:"groups"`
	Conflicts        []string             `json:"conflicts"`
}

type managedSyncApplyRequest struct {
	ContractVersion   string                      `json:"contract_version"`
	OperationID       string                      `json:"operation_id"`
	RoutePlanVersion  int64                       `json:"route_plan_version"`
	ExpectedStateHash string                      `json:"expected_state_hash"`
	Channels          []managedSyncDesiredChannel `json:"channels"`
	Groups            []managedSyncGroup          `json:"groups"`
}

type managedSyncAction struct {
	Resource  string `json:"resource"`
	Key       string `json:"key"`
	Action    string `json:"action"`
	ChannelID int64  `json:"channel_id"`
}

type managedSyncApplyResponse struct {
	OperationID      string              `json:"operation_id"`
	RoutePlanVersion int64               `json:"route_plan_version"`
	Replayed         bool                `json:"replayed"`
	Actions          []managedSyncAction `json:"actions"`
	State            managedSyncState    `json:"state"`
}

func (c *Client) ReadManagedSyncCapabilities(ctx context.Context) (reconciliation.ManagedSyncCapabilities, error) {
	var response apiResponse[managedSyncCapabilities]
	if err := c.request(ctx, http.MethodGet, "/api/manyrouter/sync/capabilities", nil, &response, false); err != nil {
		return reconciliation.ManagedSyncCapabilities{}, err
	}
	ranges, err := parseStatusCodeRanges(response.Data.RetryPolicy.StatusCodes)
	if err != nil {
		return reconciliation.ManagedSyncCapabilities{}, reconciliation.NewFailure(
			reconciliation.FailureCompatibility,
			"invalid_managed_retry_policy",
			"New API managed sync returned invalid retry status codes",
			err,
		)
	}
	return reconciliation.ManagedSyncCapabilities{
		ContractVersion: response.Data.ContractVersion,
		NewAPIVersion:   response.Data.NewAPIVersion,
		DatabaseType:    response.Data.DatabaseType,
		Features: reconciliation.ManagedSyncFeatures{
			AtomicApply:           response.Data.Features.AtomicApply,
			ManagedChannels:       response.Data.Features.ManagedChannels,
			MultipleGroups:        response.Data.Features.MultipleGroups,
			GroupRatios:           response.Data.Features.GroupRatios,
			EntryVisibility:       response.Data.Features.EntryVisibility,
			PersistentIdempotency: response.Data.Features.PersistentIdempotency,
			FinalStateDigest:      response.Data.Features.FinalStateDigest,
			LogRead:               response.Data.Features.LogRead,
		},
		Limits: reconciliation.ManagedSyncLimits{
			MaxChannels:      response.Data.Limits.MaxChannels,
			MaxGroups:        response.Data.Limits.MaxGroups,
			MaxModels:        response.Data.Limits.MaxModels,
			MaxGroupKeyBytes: response.Data.Limits.MaxGroupKeyBytes,
			MaxRequestBytes:  response.Data.Limits.MaxRequestBytes,
		},
		RetryPolicy: reconciliation.RetryPolicy{
			RetryTimes:  response.Data.RetryPolicy.RetryTimes,
			StatusCodes: ranges,
		},
		BillingBasis:     response.Data.BillingBasis,
		BillingBasisHash: response.Data.BillingBasisHash,
	}, nil
}

func (c *Client) ReadManagedState(ctx context.Context) (reconciliation.ManagedSyncState, error) {
	var response apiResponse[managedSyncState]
	if err := c.request(ctx, http.MethodGet, "/api/manyrouter/sync/state", nil, &response, false); err != nil {
		return reconciliation.ManagedSyncState{}, err
	}
	return mapManagedSyncState(response.Data)
}

func (c *Client) ApplyManagedState(ctx context.Context, request reconciliation.ManagedSyncRequest) (reconciliation.ManagedSyncResult, error) {
	body := managedSyncApplyRequest{
		ContractVersion:   reconciliation.ManagedSyncContractVersion,
		OperationID:       request.OperationID.String(),
		RoutePlanVersion:  request.RoutePlanVersion,
		ExpectedStateHash: request.ExpectedStateHash,
		Channels:          make([]managedSyncDesiredChannel, 0, len(request.Channels)),
		Groups:            make([]managedSyncGroup, 0, len(request.Groups)),
	}
	redactions := make([]string, 0, len(request.Channels))
	for _, input := range request.Channels {
		models := make([]managedSyncModel, 0, len(input.Desired.Models))
		for _, item := range input.Desired.Models {
			models = append(models, managedSyncModel{Model: item.Model, UpstreamModel: item.UpstreamModel})
		}
		key := string(input.APIKey)
		redactions = append(redactions, key)
		desiredStatus := "enabled"
		if input.Desired.DesiredStatus == routing.DesiredDisabled {
			desiredStatus = "disabled"
		}
		body.Channels = append(body.Channels, managedSyncDesiredChannel{
			ManagedTag: input.Desired.ManagedTag, Name: input.Desired.Name, BaseURL: input.Desired.BaseURL,
			APIKey: key, CredentialVersion: input.Desired.CredentialVersion, Models: models,
			Groups: input.Desired.GroupKeys(), Priority: input.Desired.Priority, Weight: input.Desired.Weight,
			DesiredStatus: desiredStatus, Resume: input.Resume,
		})
	}
	for _, group := range request.Groups {
		body.Groups = append(body.Groups, managedSyncGroup{
			Key: group.Key, DisplayName: group.DisplayName, SaleRatio: group.SaleRatio, Visible: group.Visible,
		})
	}
	var response apiResponse[managedSyncApplyResponse]
	if err := c.request(ctx, http.MethodPut, "/api/manyrouter/sync/state", body, &response, true, redactions...); err != nil {
		return reconciliation.ManagedSyncResult{}, err
	}
	if response.Data.OperationID != request.OperationID.String() || response.Data.RoutePlanVersion != request.RoutePlanVersion {
		return reconciliation.ManagedSyncResult{}, reconciliation.NewFailure(
			reconciliation.FailureCompatibility,
			"managed_sync_response_mismatch",
			"New API managed sync response did not match the requested operation",
			nil,
		)
	}
	state, err := mapManagedSyncState(response.Data.State)
	if err != nil {
		return reconciliation.ManagedSyncResult{}, err
	}
	actions := make([]reconciliation.ManagedSyncAction, 0, len(response.Data.Actions))
	for _, action := range response.Data.Actions {
		actions = append(actions, reconciliation.ManagedSyncAction{
			Resource: action.Resource, Key: action.Key, Action: action.Action, ChannelID: action.ChannelID,
		})
	}
	return reconciliation.ManagedSyncResult{Replayed: response.Data.Replayed, Actions: actions, State: state}, nil
}

func mapManagedSyncState(input managedSyncState) (reconciliation.ManagedSyncState, error) {
	if input.ContractVersion != reconciliation.ManagedSyncContractVersion || input.NewAPIVersion == "" ||
		len(input.StateHash) != 64 || len(input.BillingBasisHash) != 64 {
		return reconciliation.ManagedSyncState{}, reconciliation.NewFailure(
			reconciliation.FailureCompatibility,
			"invalid_managed_state",
			"New API managed state response is incomplete",
			nil,
		)
	}
	actual := reconciliation.ActualState{
		Version: input.NewAPIVersion, GroupRatios: make(map[string]string, len(input.Groups)),
		UserUsableGroups: make(map[string]string), Channels: make([]reconciliation.ActualChannel, 0, len(input.Channels)),
	}
	for _, group := range input.Groups {
		actual.GroupRatios[group.Key] = group.SaleRatio
		if group.Visible {
			actual.UserUsableGroups[group.Key] = group.DisplayName
		}
	}
	for _, channel := range input.Channels {
		models := make([]string, 0, len(channel.Models))
		mapping := make(map[string]string)
		for _, item := range channel.Models {
			models = append(models, item.Model)
			if item.Model != item.UpstreamModel {
				mapping[item.Model] = item.UpstreamModel
			}
		}
		sort.Strings(models)
		groups := append([]string(nil), channel.Groups...)
		sort.Strings(groups)
		status, err := mapManagedSyncStatus(channel.Status)
		if err != nil {
			return reconciliation.ManagedSyncState{}, err
		}
		actual.Channels = append(actual.Channels, reconciliation.ActualChannel{
			ID: channel.ID, ManagedTag: channel.ManagedTag, Name: channel.Name,
			Protocol: "openai_compatible", BaseURL: strings.TrimRight(channel.BaseURL, "/"),
			Models: models, ModelMapping: mapping, Groups: groups, Priority: channel.Priority,
			Weight: channel.Weight, Status: status, CredentialVersion: channel.CredentialVersion,
		})
	}
	return reconciliation.ManagedSyncState{
		StateHash: input.StateHash, BillingBasisHash: input.BillingBasisHash,
		Actual: actual, Conflicts: append([]string(nil), input.Conflicts...),
	}, nil
}

func mapManagedSyncStatus(status string) (reconciliation.ChannelStatus, error) {
	switch status {
	case "enabled":
		return reconciliation.ChannelEnabled, nil
	case "manually_disabled":
		return reconciliation.ChannelManuallyDisabled, nil
	case "auto_disabled":
		return reconciliation.ChannelAutoDisabled, nil
	default:
		return reconciliation.ChannelUnknown, fmt.Errorf("managed channel status %q is unsupported", status)
	}
}

var _ reconciliation.ManagedSyncGateway = (*Client)(nil)
