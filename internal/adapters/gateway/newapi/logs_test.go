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

func TestReadAdminLogsSendsFiltersAndParsesStableFields(t *testing.T) {
	t.Parallel()
	const other = `{"first_token_time":1.25,"stream_status":{"status":"completed"}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/log/" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer admin-token" || request.Header.Get("New-Api-User") != "1" {
			t.Errorf("missing New API administrator authentication")
		}
		query := request.URL.Query()
		want := map[string]string{
			"type": "2", "start_timestamp": "1700000000", "end_timestamp": "1700003600",
			"p": "3", "page_size": "25",
		}
		for key, value := range want {
			if query.Get(key) != value {
				t.Errorf("query %s = %q, want %q", key, query.Get(key), value)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"items": []map[string]any{{
					"id": 91, "created_at": 1700000123, "type": 2, "content": "completed", "model_name": "model-a",
					"prompt_tokens": 17, "completion_tokens": 23, "use_time": 8, "is_stream": true,
					"channel": 44, "group": "mr_s_supplier", "request_id": "request-1",
					"upstream_request_id": "upstream-1", "other": other,
				}},
				"total": 51, "page": 3, "page_size": 25,
			},
		})
	}))
	defer server.Close()

	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ReadAdminLogs(context.Background(), 2, 1700000000, 1700003600, 3, 25)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 51 || page.Page != 3 || page.PageSize != 25 || len(page.Items) != 1 {
		t.Fatalf("unexpected log page: %#v", page)
	}
	entry := page.Items[0]
	if entry.ID != 91 || entry.CreatedAt != 1700000123 || entry.Type != 2 || entry.Content != "completed" || entry.Model != "model-a" {
		t.Fatalf("unexpected log identity: %#v", entry)
	}
	if entry.InputTokens != 17 || entry.OutputTokens != 23 || entry.DurationSeconds != 8 || !entry.Stream {
		t.Fatalf("unexpected log measurements: %#v", entry)
	}
	if entry.ChannelID != 44 || entry.Group != "mr_s_supplier" || entry.RequestID != "request-1" || entry.UpstreamRequestID != "upstream-1" || entry.Other != other {
		t.Fatalf("unexpected log attribution: %#v", entry)
	}
}

func TestReadAdminLogsRejectsInvalidPaginationWithoutRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		page     int
		pageSize int
		code     string
	}{
		{name: "zero page", page: 0, pageSize: 20, code: "invalid_log_page"},
		{name: "negative page", page: -1, pageSize: 20, code: "invalid_log_page"},
		{name: "zero page size", page: 1, pageSize: 0, code: "invalid_log_page_size"},
		{name: "oversized page", page: 1, pageSize: 101, code: "invalid_log_page_size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.ReadAdminLogs(context.Background(), 0, 0, 0, test.page, test.pageSize)
			var failure *reconciliation.Failure
			if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureConfiguration || failure.Code != test.code {
				t.Fatalf("unexpected pagination failure: %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid pagination reached New API %d times", calls.Load())
	}
}

func TestReadAdminLogsPreservesBusinessErrorSemantics(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":false,"message":"administrator log query rejected"}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ReadAdminLogs(context.Background(), 2, 1, 2, 1, 20)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureConfiguration || failure.Code != "gateway_business_error" {
		t.Fatalf("unexpected business failure: %v", err)
	}
}

func TestReadAdminLogsDoesNotFollowRedirects(t *testing.T) {
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

	_, err = client.ReadAdminLogs(context.Background(), 2, 1, 2, 1, 20)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureCompatibility || failure.Code != "gateway_http_307" {
		t.Fatalf("unexpected redirect failure: %v", err)
	}
	if targetCalled.Load() {
		t.Fatal("redirect target received the privileged log request")
	}
}

func TestReadAdminLogsRejectsOversizedResponses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"items":[],"padding":"`))
		_, _ = writer.Write([]byte(strings.Repeat("x", (2<<20)+1)))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ReadAdminLogs(context.Background(), 2, 1, 2, 1, 20)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureCompatibility || failure.Code != "response_too_large" {
		t.Fatalf("unexpected oversized response failure: %v", err)
	}
}
