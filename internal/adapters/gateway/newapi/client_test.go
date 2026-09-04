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
)

func TestReadActualStateReadsOptionsAndPaginatedChannels(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer admin-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
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
