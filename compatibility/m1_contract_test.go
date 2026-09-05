//go:build contract

package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

type m1ContractSite struct {
	baseURL      string
	client       *newapi.Client
	channel      routing.DesiredChannel
	channelID    int64
	dedicated    routing.DesiredGroup
	auto         routing.DesiredGroup
	dedicatedKey string
	autoKey      string
	basisHash    string
	baseline     reconciliation.ActualState
}

func TestNewAPIM1TwoSiteGroupsAndBillingContract(t *testing.T) {
	binary := os.Getenv("MANYROUTER_NEW_API_BINARY")
	if binary == "" {
		t.Skip("MANYROUTER_NEW_API_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	upstream := newOpenAIMock(t)
	sites := make([]m1ContractSite, 0, 2)
	for index := range 2 {
		baseURL := startNewAPI(t, ctx, binary)
		admin := initializeAndLogin(t, ctx, baseURL)
		client, err := newapi.NewClient(baseURL, []byte(admin), http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		basis, hash, err := client.ReadBillingBasis(ctx)
		if err != nil {
			t.Fatalf("site %d pricing baseline: %v", index, err)
		}
		_, sameHash, err := client.ReadBillingBasis(ctx)
		if err != nil || hash != sameHash || len(hash) != 64 {
			t.Fatalf("site %d unstable pricing baseline: %v", index, err)
		}
		for _, required := range []string{"ModelRatio", "CompletionRatio", "ModelPrice", "NewAPIVersion"} {
			if len(basis[required]) == 0 {
				t.Fatalf("site %d missing pricing field %s", index, required)
			}
		}
		logContractModelPricing(t, index, basis)
		baseline, err := client.ReadActualState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		relationID := uuid.New()
		ratio, autoRatio := "1.25", "1.5"
		if index == 1 {
			ratio, autoRatio = "2.5", "3"
		}
		dedicated := routing.DesiredGroup{Key: routing.GroupKey(relationID), DisplayName: fmt.Sprintf("Contract Site %d Supplier", index), SaleRatio: ratio, Visible: true}
		auto := routing.DesiredGroup{Key: "mr_a_contract_shared", DisplayName: "Contract Auto", SaleRatio: autoRatio, Visible: true}
		channel := routing.DesiredChannel{
			ID: uuid.New(), ManagedTag: routing.ManagedTag(relationID), Name: fmt.Sprintf("Contract Site %d [ManyRouter]", index),
			Protocol: "openai_compatible", BaseURL: upstream.URL, CredentialID: uuid.New(), CredentialVersion: 1,
			Models: []routing.ModelRoute{{Model: contractModel, UpstreamModel: contractModel}}, GroupKey: dedicated.Key, ExtraGroupKeys: []string{auto.Key},
			Priority: 0, Weight: 100, DesiredStatus: routing.DesiredEnabled,
		}
		ratios, _, err := reconciliation.MergeGroupRatios(baseline.GroupRatios, dedicated.Key, dedicated.SaleRatio)
		if err != nil {
			t.Fatal(err)
		}
		ratios, _, err = reconciliation.MergeGroupRatios(ratios, auto.Key, auto.SaleRatio)
		if err != nil {
			t.Fatal(err)
		}
		if err = client.SetGroupRatios(ctx, ratios); err != nil {
			t.Fatal(err)
		}
		visible, _ := reconciliation.MergeUserUsableGroups(baseline.UserUsableGroups, dedicated)
		visible, _ = reconciliation.MergeUserUsableGroups(visible, auto)
		if err = client.SetUserUsableGroups(ctx, visible); err != nil {
			t.Fatal(err)
		}
		if err = client.CreateChannel(ctx, channel, []byte(contractUpstreamKey)); err != nil {
			t.Fatal(err)
		}
		actual, err := client.ReadActualState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		created, err := reconciliation.LocateManagedChannel(channel, nil, actual.Channels)
		if err != nil || created == nil {
			t.Fatalf("site %d missing channel: %v", index, err)
		}
		if err = client.TestChannel(ctx, created.ID, contractModel, []byte(contractUpstreamKey)); err != nil {
			t.Fatal(err)
		}
		if err = client.UpdateChannel(ctx, created.ID, channel, []byte(contractUpstreamKey)); err != nil {
			t.Fatal(err)
		}
		if err = client.SetChannelEnabled(ctx, created.ID, true); err != nil {
			t.Fatal(err)
		}
		site := m1ContractSite{baseURL: baseURL, client: client, channel: channel, channelID: created.ID, dedicated: dedicated, auto: auto, basisHash: hash, baseline: baseline}
		site.dedicatedKey = createUserAPIKey(t, ctx, baseURL, admin, dedicated.Key)
		site.autoKey = createUserAPIKey(t, ctx, baseURL, admin, auto.Key)
		logM1ResolvedPricing(t, ctx, index, baseURL, admin)
		quotaBefore := m1UsedQuota(t, ctx, baseURL, admin)
		for _, key := range []string{site.dedicatedKey, site.autoKey} {
			assertM1Completion(t, ctx, site.baseURL, key, http.StatusOK)
		}
		quotaAfter := m1UsedQuota(t, ctx, baseURL, admin)
		t.Logf("site %d controlled user usage for dedicated and Auto calls: %d quota units", index, quotaAfter-quotaBefore)
		sites = append(sites, site)
	}
	for index := range sites {
		site := &sites[index]
		actual, err := site.client.ReadActualState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		channel, err := reconciliation.LocateManagedChannel(site.channel, &site.channelID, actual.Channels)
		if err != nil || channel == nil {
			t.Fatalf("site %d channel disappeared: %v", index, err)
		}
		if !slices.Equal(channel.Groups, site.channel.GroupKeys()) {
			t.Fatalf("site %d channel group membership differs: %#v", index, channel.Groups)
		}
		if actual.GroupRatios[site.dedicated.Key] != site.dedicated.SaleRatio || actual.GroupRatios[site.auto.Key] != site.auto.SaleRatio {
			t.Fatalf("site %d price leaked between sites", index)
		}
		if _, exists := actual.GroupRatios[sites[1-index].dedicated.Key]; exists {
			t.Fatalf("site %d contains the other site's dedicated group", index)
		}
		_, hash, err := site.client.ReadBillingBasis(ctx)
		if err != nil || hash != site.basisHash {
			t.Fatalf("site %d managed group updates changed pricing baseline: %v", index, err)
		}
		assertM1UnmanagedGroups(t, site.baseline, actual, site.dedicated.Key, site.auto.Key)
	}
	// Record the gateway's existing-key behavior separately from presentation visibility.
	hidden := sites[1].dedicated
	hidden.Visible = false
	actual, err := sites[1].client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	visible, _ := reconciliation.MergeUserUsableGroups(actual.UserUsableGroups, hidden)
	if err = sites[1].client.SetUserUsableGroups(ctx, visible); err != nil {
		t.Fatal(err)
	}
	hiddenStatus, hiddenError := m1Completion(ctx, sites[1].baseURL, sites[1].dedicatedKey)
	t.Logf("hidden dedicated group with an existing user key: HTTP %d; error code %q", hiddenStatus, hiddenError)
	if hiddenStatus != http.StatusForbidden || hiddenError != "new_api_error" {
		t.Fatalf("unexpected hidden-group key outcome: HTTP %d, %s", hiddenStatus, hiddenError)
	}
	assertM1Completion(t, ctx, sites[0].baseURL, sites[0].dedicatedKey, http.StatusOK)
	assertM1Completion(t, ctx, sites[1].baseURL, sites[1].autoKey, http.StatusOK)
	firstState, err := sites[0].client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := sites[1].client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := firstState.UserUsableGroups[sites[0].dedicated.Key]; !ok {
		t.Fatal("hiding the second site's group changed the first site")
	}
	if _, ok := secondState.UserUsableGroups[sites[1].dedicated.Key]; ok {
		t.Fatal("hidden dedicated group is still user-selectable")
	}
	// Remove only the first site's Auto membership; its dedicated route remains usable.
	sites[0].channel.ExtraGroupKeys = nil
	if err = sites[0].client.UpdateChannel(ctx, sites[0].channelID, sites[0].channel, []byte(contractUpstreamKey)); err != nil {
		t.Fatal(err)
	}
	assertM1Completion(t, ctx, sites[0].baseURL, sites[0].dedicatedKey, http.StatusOK)
	emptyStatus, emptyCode := m1Completion(ctx, sites[0].baseURL, sites[0].autoKey)
	t.Logf("Auto group after its last member was removed: HTTP %d; error code %q", emptyStatus, emptyCode)
	if emptyStatus != http.StatusServiceUnavailable || emptyCode != "model_not_found" {
		t.Fatalf("empty Auto group still routed a user request: status=%d code=%s", emptyStatus, emptyCode)
	}
	assertM1Completion(t, ctx, sites[1].baseURL, sites[1].autoKey, http.StatusOK)
	for index := range sites {
		actual, err := sites[index].client.ReadActualState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertM1UnmanagedGroups(t, sites[index].baseline, actual, sites[index].dedicated.Key, sites[index].auto.Key)
	}
}

func m1Completion(ctx context.Context, baseURL, key string) (int, string) {
	body, err := json.Marshal(map[string]any{
		"model":      contractModel,
		"messages":   []map[string]string{{"role": "user", "content": "M1 contract check"}},
		"max_tokens": 1,
	})
	if err != nil {
		return 0, "encode request"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "transport error"
	}
	defer func() { _ = response.Body.Close() }()
	var result struct {
		ID    string `json:"id"`
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return response.StatusCode, "invalid JSON"
	}
	if response.StatusCode == http.StatusOK && result.ID == "" {
		return response.StatusCode, "missing completion ID"
	}
	if result.Error.Code != "" {
		return response.StatusCode, result.Error.Code
	}
	return response.StatusCode, result.Error.Type
}

func assertM1Completion(t *testing.T, ctx context.Context, baseURL, key string, want int) {
	t.Helper()
	status, code := m1Completion(ctx, baseURL, key)
	if status != want || (want == http.StatusOK && code != "") {
		t.Fatalf("user completion HTTP %d, want %d; code %q", status, want, code)
	}
}

func assertM1UnmanagedGroups(t *testing.T, before, after reconciliation.ActualState, managed ...string) {
	t.Helper()
	for key, value := range before.GroupRatios {
		if !slices.Contains(managed, key) && after.GroupRatios[key] != value {
			t.Fatalf("unmanaged group price %s changed", key)
		}
	}
	for key, value := range before.UserUsableGroups {
		if !slices.Contains(managed, key) && after.UserUsableGroups[key] != value {
			t.Fatalf("unmanaged user group %s changed", key)
		}
	}
}

func logContractModelPricing(t *testing.T, siteIndex int, basis map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"ModelRatio", "CompletionRatio", "ModelPrice"} {
		var models map[string]json.RawMessage
		if err := json.Unmarshal(basis[key], &models); err != nil {
			t.Fatalf("invalid %s pricing object: %v", key, err)
		}
		if raw, exists := models[contractModel]; exists {
			t.Logf("site %d published %s for %s: %s", siteIndex, key, contractModel, raw)
		} else {
			t.Logf("site %d %s has no explicit entry for %s", siteIndex, key, contractModel)
		}
	}
}

func logM1ResolvedPricing(t *testing.T, ctx context.Context, siteIndex int, baseURL, adminToken string) {
	t.Helper()
	var pricing apiEnvelope[[]struct {
		ModelName       string      `json:"model_name"`
		QuotaType       int         `json:"quota_type"`
		ModelRatio      json.Number `json:"model_ratio"`
		CompletionRatio json.Number `json:"completion_ratio"`
		ModelPrice      json.Number `json:"model_price"`
		BillingMode     string      `json:"billing_mode"`
	}]
	getJSON(t, ctx, baseURL+"/api/pricing", adminToken, &pricing)
	if !pricing.Success {
		t.Fatal("New API did not return resolved model pricing")
	}
	for _, model := range pricing.Data {
		if model.ModelName != contractModel {
			continue
		}
		if model.ModelRatio == "" || model.CompletionRatio == "" {
			t.Fatal("resolved token pricing omitted a required multiplier")
		}
		t.Logf("site %d resolved model pricing: model=%s quota_type=%d model_ratio=%s completion_ratio=%s model_price=%s billing_mode=%q", siteIndex, model.ModelName, model.QuotaType, model.ModelRatio, model.CompletionRatio, model.ModelPrice, model.BillingMode)
		return
	}
	t.Fatal("New API resolved pricing did not include the enabled contract model")
}

func m1UsedQuota(t *testing.T, ctx context.Context, baseURL, adminToken string) int64 {
	t.Helper()
	var profile apiEnvelope[struct {
		UsedQuota *int64 `json:"used_quota"`
	}]
	getJSON(t, ctx, baseURL+"/api/user/self", adminToken, &profile)
	if !profile.Success || profile.Data.UsedQuota == nil {
		t.Fatal("New API user usage counter was unavailable")
	}
	return *profile.Data.UsedQuota
}
