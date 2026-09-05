package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

type fakeOperations struct {
	mutation operations.Mutation
	filter   operations.Filter
	kind     string
	calls    int
	err      error
}

func (f *fakeOperations) List(_ context.Context, kind string, filter operations.Filter) (operations.Page, error) {
	f.kind, f.filter = kind, filter
	return operations.Page{Items: []json.RawMessage{json.RawMessage(`{"sale_ratio":"1.250000"}`)}, Total: 1, Offset: filter.Offset, Limit: filter.Limit}, f.err
}
func (f *fakeOperations) Get(_ context.Context, kind string, id uuid.UUID) (json.RawMessage, error) {
	f.kind = kind
	return json.RawMessage(`{"id":"` + id.String() + `"}`), f.err
}
func (f *fakeOperations) Execute(_ context.Context, mutation operations.Mutation) (json.RawMessage, error) {
	f.calls++
	f.mutation = mutation
	return json.RawMessage(`{"plans":[]}`), f.err
}

func TestOperationsRouteMutationContract(t *testing.T) {
	service := &fakeOperations{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := &Handler{operations: service, logger: logger}
	router, err := NewRouter(handler, strings.Repeat("t", 32), logger)
	if err != nil {
		t.Fatal(err)
	}
	RegisterOperationsRoutes(router, handler)
	id := uuid.NewString()
	cases := []struct {
		method, path, kind, body string
		status                   int
	}{
		{http.MethodPost, "/sites", "create_site", `{"code":"site","name":"Site","new_api_base_url":"https://site.test","new_api_access_token":"secret-token","admin_user_id":7}`, http.StatusCreated},
		{http.MethodPut, "/sites/" + id, "update_site", `{"name":"Site","new_api_base_url":"https://site.test","status":"enabled","version":2,"reason":"edit"}`, http.StatusOK},
		{http.MethodPost, "/suppliers", "create_supplier", `{"code":"supplier","name":"Supplier","upstream_base_url":"https://upstream.test","upstream_api_key":"secret-key","models":[{"model":"a","upstream_model":"b","input_price":"0.001","output_price":"0.002","currency":"USD","enabled":true}]}`, http.StatusCreated},
		{http.MethodPut, "/suppliers/" + id, "update_supplier", `{"name":"Supplier","upstream_base_url":"https://upstream.test","models":[],"status":"enabled","version":2,"reason":"edit"}`, http.StatusOK},
		{http.MethodPost, "/suppliers/" + id + "/credentials", "rotate_credential", `{"version":2,"api_key":"new-secret","reason":"rotation"}`, http.StatusOK},
		{http.MethodPost, "/suppliers/" + id + "/credentials/cancel", "cancel_credential", `{"version":2,"reason":"cancel"}`, http.StatusOK},
		{http.MethodPost, "/deployments", "deploy", `{"supplier_id":"` + id + `","sites":[],"reason":"deployment"}`, http.StatusAccepted},
		{http.MethodPut, "/relations/" + id, "relation", `{"version":2,"group_display_name":"Group","resume":true,"visible":false,"desired_status":"enabled","reason":"edit"}`, http.StatusOK},
		{http.MethodPut, "/sites/" + id + "/strategies/balanced", "strategy", `{"version":0,"enabled":false,"visible":false,"display_name":"Balanced","member_relation_ids":["` + id + `"],"reason":"edit"}`, http.StatusOK},
		{http.MethodPost, "/prices", "draft_price", `{"site_id":"` + id + `","group_key":"group","sale_ratio":"1.250000","reason":"adjust"}`, http.StatusCreated},
		{http.MethodPost, "/prices/" + id + "/publish", "publish_price", `{"version":2}`, http.StatusOK},
		{http.MethodPost, "/plans/" + id + "/restore", "restore", `{"reason":"restore"}`, http.StatusOK},
		{http.MethodPost, "/sites/" + id + "/sync", "sync", `{}`, http.StatusAccepted},
	}
	for _, test := range cases {
		t.Run(test.kind, func(t *testing.T) {
			req := httptest.NewRequest(test.method, managementAPI+"/ops"+test.path, strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			req.Header.Set("Idempotency-Key", "operation-123")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != test.status || service.mutation.Kind != test.kind || service.mutation.Key != "operation-123" || service.mutation.Actor != "deployment-owner" {
				t.Fatalf("mutation routing failed: %d %s", response.Code, response.Body.String())
			}
			if test.kind == "strategy" && service.mutation.StrategyKind != "balanced" {
				t.Fatal("strategy path identity lost")
			}
			if test.kind == "draft_price" && service.mutation.Input.(operations.PriceInput).SaleRatio != "1.250000" {
				t.Fatal("decimal price changed during decoding")
			}
			if test.kind == "sync" && service.mutation.Input != nil {
				t.Fatal("sync must not invent business input")
			}
		})
	}
}

func TestOperationsRequestRejectionAndErrors(t *testing.T) {
	service := &fakeOperations{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := &Handler{operations: service, logger: logger}
	router, err := NewRouter(handler, strings.Repeat("t", 32), logger)
	if err != nil {
		t.Fatal(err)
	}
	RegisterOperationsRoutes(router, handler)
	cases := []struct {
		path, body, key, authorization string
		status                         int
	}{
		{"/sites", `{}`, "", "Bearer " + strings.Repeat("t", 32), http.StatusBadRequest},
		{"/sites", `{"code":"site","name":"Site","new_api_base_url":"https://site.test","new_api_access_token":"secret-token","unknown":"secret"}`, "operation-123", "Bearer " + strings.Repeat("t", 32), http.StatusBadRequest},
		{"/sites", `{} {}`, "operation-123", "Bearer " + strings.Repeat("t", 32), http.StatusBadRequest},
		{"/sites/invalid/sync", `{}`, "operation-123", "Bearer " + strings.Repeat("t", 32), http.StatusBadRequest},
		{"/sites", `{}`, "operation-123", "", http.StatusUnauthorized},
		{"/prices", `{"sale_ratio":1.25}`, "operation-123", "Bearer " + strings.Repeat("t", 32), http.StatusBadRequest},
	}
	for _, test := range cases {
		req := httptest.NewRequest(http.MethodPost, managementAPI+"/ops"+test.path, strings.NewReader(test.body))
		req.Header.Set("Authorization", test.authorization)
		req.Header.Set("Idempotency-Key", test.key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("request rejection: %d %s", response.Code, response.Body.String())
		}
	}
	if service.calls != 0 {
		t.Fatal("rejected request reached application")
	}
	for _, test := range []struct {
		err    error
		status int
	}{{operations.ErrInvalid, 400}, {operations.ErrConflict, 409}, {operations.ErrBusy, 409}, {operations.ErrNotFound, 404}, {errors.New("internal failure"), 500}} {
		service.err = test.err
		req := httptest.NewRequest(http.MethodPost, managementAPI+"/ops/sites", strings.NewReader(`{"code":"site","name":"Site","new_api_base_url":"https://site.test","new_api_access_token":"secret-token"}`))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		req.Header.Set("Idempotency-Key", "operation-123")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"request_id"`) {
			t.Fatalf("error mapping: %d %s", response.Code, response.Body.String())
		}
		if test.status == 500 && strings.Contains(response.Body.String(), "internal failure") {
			t.Fatal("internal error leaked")
		}
	}
}

func TestOperationsPaginationAndDetail(t *testing.T) {
	service := &fakeOperations{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := &Handler{operations: service, logger: logger}
	router, err := NewRouter(handler, strings.Repeat("t", 32), logger)
	if err != nil {
		t.Fatal(err)
	}
	RegisterOperationsRoutes(router, handler)
	id := uuid.New()
	for _, path := range []string{"/prices?q=model&site_id=" + id.String() + "&limit=10&offset=20", "/plans/" + id.String()} {
		req := httptest.NewRequest(http.MethodGet, managementAPI+"/ops"+path, nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("read route: %d", response.Code)
		}
	}
	if service.filter.Query != "model" || service.filter.SiteID != id || service.filter.Limit != 10 || service.filter.Offset != 20 || service.kind != "plans" {
		t.Fatal("query or resource identity lost")
	}
	for _, query := range []string{"limit=no", "offset=-1", "site_id=no"} {
		req := httptest.NewRequest(http.MethodGet, managementAPI+"/ops/sites?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid query: %d", response.Code)
		}
	}
}
