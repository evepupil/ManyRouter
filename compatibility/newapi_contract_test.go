//go:build contract

package compatibility_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

func TestNewAPIExistingAdminContract(t *testing.T) {
	binary := os.Getenv("MANYROUTER_NEW_API_BINARY")
	if binary == "" {
		t.Skip("MANYROUTER_NEW_API_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	upstream := newOpenAIMock(t)
	baseURL := startNewAPI(t, ctx, binary)
	adminToken := initializeAndLogin(t, ctx, baseURL)

	client, err := newapi.NewClient(baseURL, []byte(adminToken), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	relationID := uuid.New()
	desired := routing.Snapshot{
		SchemaVersion: routing.SnapshotSchemaVersion,
		SiteID:        uuid.New(),
		RelationID:    relationID,
		SupplierID:    uuid.New(),
		Group: routing.DesiredGroup{
			Key: routing.GroupKey(relationID), DisplayName: "ManyRouter Contract", SaleRatio: "1.25", Visible: true,
		},
		Channel: routing.DesiredChannel{
			ID: uuid.New(), ManagedTag: routing.ManagedTag(relationID), Name: "ManyRouter Contract [ManyRouter]",
			Protocol: "openai_compatible", BaseURL: upstream.URL, CredentialID: uuid.New(), CredentialVersion: 1,
			Models:   []routing.ModelRoute{{Model: contractModel, UpstreamModel: contractModel}},
			GroupKey: routing.GroupKey(relationID), Priority: 0, Weight: 100, DesiredStatus: routing.DesiredEnabled,
		},
	}
	if err := routing.ValidateSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	ratios, _, err := reconciliation.MergeGroupRatios(actual.GroupRatios, desired.Group.Key, desired.Group.SaleRatio)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetGroupRatios(ctx, ratios); err != nil {
		t.Fatal(err)
	}
	userGroups, _ := reconciliation.MergeUserUsableGroups(actual.UserUsableGroups, desired.Group)
	if err := client.SetUserUsableGroups(ctx, userGroups); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateChannel(ctx, desired.Channel, []byte(contractUpstreamKey)); err != nil {
		t.Fatal(err)
	}
	actual, err = client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := reconciliation.LocateManagedChannel(desired.Channel, nil, actual.Channels)
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil {
		t.Fatal("New API did not expose the created managed channel")
	}
	if err := client.TestChannel(ctx, channel.ID, contractModel); err != nil {
		t.Fatal(err)
	}
	if err := client.SetChannelEnabled(ctx, channel.ID, true); err != nil {
		t.Fatal(err)
	}
	actual, err = client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channelID := channel.ID
	if _, err := reconciliation.Verify(desired, actual, &channelID); err != nil {
		t.Fatal(err)
	}

	userAPIKey := createUserAPIKey(t, ctx, baseURL, adminToken, desired.Group.Key)
	requestBody := map[string]any{
		"model":      contractModel,
		"messages":   []map[string]string{{"role": "user", "content": "contract check"}},
		"max_tokens": 1,
	}
	var completion map[string]any
	postJSON(t, ctx, baseURL+"/v1/chat/completions", userAPIKey, requestBody, &completion)
	if completion["id"] == nil {
		t.Fatalf("New API relay response did not include an ID: %#v", completion)
	}
}

func initializeAndLogin(t *testing.T, ctx context.Context, baseURL string) string {
	t.Helper()
	setupRequest := map[string]any{
		"username": contractUsername, "password": contractPassword, "confirmPassword": contractPassword,
		"SelfUseModeEnabled": true, "DemoSiteEnabled": false,
	}
	var setupResponse apiEnvelope[json.RawMessage]
	postJSON(t, ctx, baseURL+"/api/setup", "", setupRequest, &setupResponse)
	if !setupResponse.Success {
		t.Fatalf("New API setup failed: %s", setupResponse.Message)
	}
	var loginResponse apiEnvelope[struct {
		AccessToken string `json:"access_token"`
	}]
	postJSON(t, ctx, baseURL+"/api/user/login", "", map[string]string{
		"username": contractUsername, "password": contractPassword,
	}, &loginResponse)
	if !loginResponse.Success || loginResponse.Data.AccessToken == "" {
		t.Fatalf("New API login failed: %s", loginResponse.Message)
	}
	var optionResponse apiEnvelope[json.RawMessage]
	putJSON(t, ctx, baseURL+"/api/option/", loginResponse.Data.AccessToken, map[string]any{
		"key": "performance_setting.monitor_enabled", "value": false,
	}, &optionResponse)
	if !optionResponse.Success {
		t.Fatalf("disable New API contract-test performance guard failed: %s", optionResponse.Message)
	}
	return loginResponse.Data.AccessToken
}

func createUserAPIKey(t *testing.T, ctx context.Context, baseURL, adminToken, group string) string {
	t.Helper()
	name := "manyrouter-contract-" + uuid.NewString()[:8]
	var createResponse apiEnvelope[json.RawMessage]
	postJSON(t, ctx, baseURL+"/api/token/", adminToken, map[string]any{
		"name": name, "expired_time": -1, "remain_quota": 0, "unlimited_quota": true,
		"model_limits_enabled": false, "model_limits": "", "group": group,
	}, &createResponse)
	if !createResponse.Success {
		t.Fatalf("create New API user key failed: %s", createResponse.Message)
	}
	var listResponse apiEnvelope[struct {
		Items []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}]
	getJSON(t, ctx, baseURL+"/api/token/?p=1&page_size=100", adminToken, &listResponse)
	for _, item := range listResponse.Data.Items {
		if item.Name != name {
			continue
		}
		var keyResponse apiEnvelope[struct {
			Key string `json:"key"`
		}]
		postJSON(t, ctx, fmt.Sprintf("%s/api/token/%d/key", baseURL, item.ID), adminToken, map[string]any{}, &keyResponse)
		if keyResponse.Success && keyResponse.Data.Key != "" {
			return keyResponse.Data.Key
		}
	}
	t.Fatal("created New API user key could not be read back")
	return ""
}

type apiEnvelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

const (
	contractUsername    = "mrcontract"
	contractPassword    = "contract-password-2026"
	contractModel       = "gpt-3.5-turbo"
	contractUpstreamKey = "contract-upstream-key"
)
