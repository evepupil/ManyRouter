package newapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

func TestReadActualStateReadsOptionsAndPaginatedChannels(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer admin-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("New-Api-User") != "1" {
			http.Error(writer, "missing legacy root user ID", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/status":
			_, _ = writer.Write([]byte(`{"success":true,"message":"","data":{"version":"0ed497f0"}}`))
		case "/api/option/":
			_, _ = writer.Write([]byte(`{"success":true,"message":"","data":[{"key":"GroupRatio","value":"{\"default\":1,\"mr_s_test\":1.25}"},{"key":"UserUsableGroups","value":"{\"default\":\"Default\",\"mr_s_test\":\"Supplier\"}"}]}`))
		case "/api/channel/":
			_, _ = writer.Write([]byte(`{"success":true,"message":"","data":{"items":[{"id":7,"type":1,"status":1,"name":"Supplier [ManyRouter]","weight":100,"base_url":"https://upstream.example/v1","models":"model-b,model-a","group":"mr_s_test","model_mapping":"{\"model-a\":\"upstream-a\"}","priority":0,"tag":"manyrouter:test"}],"total":1,"page":1,"page_size":100}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.ReadActualState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != "0ed497f0" || state.GroupRatios["mr_s_test"] != "1.25" || state.UserUsableGroups["mr_s_test"] != "Supplier" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if len(state.Channels) != 1 || state.Channels[0].Models[0] != "model-a" || state.Channels[0].Status != reconciliation.ChannelEnabled {
		t.Fatalf("unexpected channels: %#v", state.Channels)
	}
}

func TestManagedSyncClientUsesNarrowContract(t *testing.T) {
	t.Parallel()
	const token = "managed-sync-token-value"
	const relationID = "11111111-1111-1111-1111-111111111111"
	const stateHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const finalHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const billingHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	var applyCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/manyrouter/sync/capabilities":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"contract_version":"m4-managed-sync-v1","new_api_version":"new-api-test","database_type":"postgres","billing_basis":{"ModelRatio":{},"CompletionRatio":{},"ModelPrice":{}},"billing_basis_hash":"` + billingHash + `","features":{"atomic_apply":true,"managed_channels":true,"multiple_groups":true,"group_ratios":true,"entry_visibility":true,"persistent_idempotency":true,"final_state_digest":true,"log_read":true},"limits":{"max_channels":100,"max_groups":20,"max_models":500,"max_group_key_bytes":64,"max_request_bytes":2097152},"retry_policy":{"retry_times":1,"status_codes":"500-503"}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/manyrouter/sync/state":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"contract_version":"m4-managed-sync-v1","new_api_version":"new-api-test","state_hash":"` + stateHash + `","billing_basis_hash":"` + billingHash + `","channels":[],"groups":[],"conflicts":[]}}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/manyrouter/sync/state":
			applyCalls.Add(1)
			var body struct {
				OperationID      string `json:"operation_id"`
				RoutePlanVersion int64  `json:"route_plan_version"`
				Channels         []struct {
					APIKey string `json:"api_key"`
				} `json:"channels"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.OperationID == "" || body.RoutePlanVersion != 7 || len(body.Channels) != 1 || body.Channels[0].APIKey != "supplier-secret" {
				t.Errorf("unexpected managed sync request: %#v", body)
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":{"operation_id":"` + body.OperationID + `","route_plan_version":7,"replayed":false,"actions":[{"resource":"channel","key":"manyrouter:` + relationID + `","action":"created","channel_id":42}],"state":{"contract_version":"m4-managed-sync-v1","new_api_version":"new-api-test","state_hash":"` + finalHash + `","billing_basis_hash":"` + billingHash + `","channels":[{"id":42,"managed_tag":"manyrouter:` + relationID + `","name":"Supplier","base_url":"https://upstream.example","credential_version":2,"models":[{"model":"model-a","upstream_model":"upstream-a"}],"groups":["mr_s_11111111111111111111111111111111"],"priority":0,"weight":100,"status":"enabled"}],"groups":[{"key":"mr_s_11111111111111111111111111111111","display_name":"Supplier","sale_ratio":"1.2","visible":true}],"conflicts":[]}}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newapi.NewClient(server.URL, []byte(token), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := client.ReadManagedSyncCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.ContractVersion != reconciliation.ManagedSyncContractVersion || capabilities.NewAPIVersion != "new-api-test" ||
		capabilities.BillingBasisHash != billingHash || capabilities.RetryPolicy.RetryTimes != 1 || !capabilities.RetryPolicy.AllowsStatus(500) {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
	basis, hash, err := client.ReadBillingBasis(context.Background())
	if err != nil || len(basis) != 3 || hash != billingHash {
		t.Fatalf("managed billing basis was not used: basis=%#v hash=%q err=%v", basis, hash, err)
	}
	state, err := client.ReadManagedState(context.Background())
	if err != nil || state.StateHash != stateHash || state.Actual.Version != "new-api-test" {
		t.Fatalf("unexpected managed state: %#v err=%v", state, err)
	}
	operationID := uuid.New()
	desired := routing.DesiredChannel{
		ManagedTag: "manyrouter:" + relationID, Name: "Supplier", Protocol: "openai_compatible",
		BaseURL: "https://upstream.example", CredentialVersion: 2,
		Models:   []routing.ModelRoute{{Model: "model-a", UpstreamModel: "upstream-a"}},
		GroupKey: "mr_s_11111111111111111111111111111111", Weight: 100, DesiredStatus: routing.DesiredEnabled,
	}
	result, err := client.ApplyManagedState(context.Background(), reconciliation.ManagedSyncRequest{
		OperationID: operationID, RoutePlanVersion: 7, ExpectedStateHash: stateHash,
		Channels: []reconciliation.ManagedSyncChannel{{Desired: desired, APIKey: []byte("supplier-secret")}},
		Groups:   []routing.DesiredGroup{{Key: desired.GroupKey, DisplayName: "Supplier", SaleRatio: "1.2", Visible: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applyCalls.Load() != 1 || result.State.StateHash != finalHash || len(result.State.Actual.Channels) != 1 ||
		result.State.Actual.Channels[0].CredentialVersion != 2 || result.State.Actual.Channels[0].Status != reconciliation.ChannelEnabled {
		t.Fatalf("unexpected managed apply result: %#v", result)
	}
}

func TestSetGroupRatiosSendsJSONNumbers(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Key != "GroupRatio" || strings.Contains(payload.Value, `"1.25"`) {
			t.Errorf("unexpected option payload: %#v", payload)
		}
		var ratios map[string]float64
		if err := json.Unmarshal([]byte(payload.Value), &ratios); err != nil {
			t.Error(err)
		}
		if ratios["managed"] != 1.25 || ratios["default"] != 1 {
			t.Errorf("unexpected ratios: %#v", ratios)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"message":""}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetGroupRatios(context.Background(), map[string]string{"managed": "1.250000", "default": "1"}); err != nil {
		t.Fatal(err)
	}
}

func TestSetUserUsableGroupsPreservesDescriptions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		var groups map[string]string
		if payload.Key != "UserUsableGroups" || json.Unmarshal([]byte(payload.Value), &groups) != nil {
			t.Errorf("unexpected user group payload: %#v", payload)
		}
		if groups["default"] != "Default" || groups["managed"] != "Supplier" {
			t.Errorf("user group descriptions changed: %#v", groups)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"message":""}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetUserUsableGroups(context.Background(), map[string]string{"default": "Default", "managed": "Supplier"}); err != nil {
		t.Fatal(err)
	}
}

func TestBusinessErrorRedactsSecrets(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":false,"message":"token admin-token is invalid"}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Probe(context.Background())
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("expected classified failure, got %v", err)
	}
	if strings.Contains(failure.Error(), "admin-token") {
		t.Fatalf("secret leaked in error: %v", failure)
	}
}

func TestChannelFailureRedactsSupplierCredential(t *testing.T) {
	t.Parallel()
	const supplierKey = "supplier-key-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":false,"message":"Incorrect API key provided: ` + supplierKey + `"}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.TestChannel(context.Background(), 42, "model-a", []byte(supplierKey))
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("expected classified failure, got %v", err)
	}
	if strings.Contains(failure.Error(), supplierKey) || !strings.Contains(failure.Error(), "[redacted]") {
		t.Fatalf("supplier credential was not redacted: %v", failure)
	}
}

func TestClientDoesNotFollowRedirectsWithAdminToken(t *testing.T) {
	t.Parallel()
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalled.Store(true)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := newapi.NewClient(redirect.URL, []byte("admin-token"), redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Probe(context.Background())
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureCompatibility {
		t.Fatalf("expected redirect compatibility failure, got %v", err)
	}
	if targetCalled.Load() {
		t.Fatal("redirect target received the privileged request")
	}
}

func TestHTTPStatusFailureClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		kind       reconciliation.FailureKind
		code       string
	}{
		{name: "rate limit is retryable", statusCode: http.StatusTooManyRequests, kind: reconciliation.FailureRetryable, code: "gateway_http_429"},
		{name: "bad request is compatibility", statusCode: http.StatusBadRequest, kind: reconciliation.FailureCompatibility, code: "gateway_http_400"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.statusCode)
			}))
			defer server.Close()
			client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
			if err != nil {
				t.Fatal(err)
			}

			_, err = client.Probe(context.Background())
			var failure *reconciliation.Failure
			if !errors.As(err, &failure) {
				t.Fatalf("expected classified failure, got %v", err)
			}
			if failure.Kind != test.kind || failure.Code != test.code {
				t.Fatalf("unexpected failure: kind=%q code=%q", failure.Kind, failure.Code)
			}
		})
	}
}
