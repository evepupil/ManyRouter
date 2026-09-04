//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/jobs"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/shopspring/decimal"
)

func TestRiverRunsM0SynchronizationAndLeavesGatewayIndependent(t *testing.T) {
	databaseURL := os.Getenv("MANYROUTER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MANYROUTER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	gateway := newFakeNewAPI()
	server := httptest.NewServer(gateway)
	defer server.Close()
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{0x42}, platformcrypto.MasterKeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	onboardingService, err := onboarding.NewService(store, vault, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	siteData, err := onboardingService.CreateSite(ctx, onboarding.CreateSiteCommand{
		Code: "sync-site-" + suffix, Name: "Synchronization Site", NewAPIBaseURL: server.URL, NewAPIAccessToken: fakeNewAPIAdminToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	supplierData, err := onboardingService.CreateSupplier(ctx, onboarding.CreateSupplierCommand{
		Code: "sync-supplier-" + suffix, Name: "Synchronization Supplier", UpstreamBaseURL: "https://upstream.example/v1", UpstreamAPIKey: fakeSupplierKey,
		Models: []supplier.ModelInput{{Name: "model-a", UpstreamName: "model-a", InputPrice: decimal.Zero, OutputPrice: decimal.Zero, Currency: "USD"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	relation, _, err := onboardingService.CreateRelation(ctx, onboarding.CreateRelationCommand{
		SiteID: siteData.ID, SupplierID: supplierData.ID, GroupDisplayName: "Synchronization Supplier", SaleRatio: decimal.RequireFromString("1.25"), Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	dispatcher := jobs.NewDispatcher()
	synchronizationService, err := reconciliation.NewService(store, vault, newapi.Factory{HTTPClient: server.Client()}, dispatcher, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	riverClient, err := jobs.NewClient(store.Pool(), synchronizationService, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Bind(riverClient); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := riverClient.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	if err := riverClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if stopped {
			return
		}
		stopContext, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = riverClient.Stop(stopContext)
	}()

	operation, err := synchronizationService.RequestSync(ctx, relation.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Job == nil || event.Job.Kind != (jobs.ReconciliationArgs{}).Kind() {
			t.Fatalf("unexpected River event: %#v", event)
		}
	case <-ctx.Done():
		t.Fatalf("synchronization job did not complete: %v", context.Cause(ctx))
	}
	operation, err = synchronizationService.GetOperation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != reconciliation.OperationSucceeded {
		t.Fatalf("synchronization status = %q, error=%v", operation.Status, operation.LastErrorMessage)
	}
	storedRelation, err := onboardingService.GetRelation(ctx, relation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRelation.SyncStatus != "active" || storedRelation.LastConfirmedAt == nil {
		t.Fatalf("site supplier was not confirmed: %#v", storedRelation)
	}
	if gateway.createCount() != 1 || !gateway.isEnabled() || gateway.groupRatio(relation.GroupKey) != "1.25" || !gateway.groupVisible(relation.GroupKey) {
		t.Fatalf("gateway state is incomplete: create=%d enabled=%v ratio=%q visible=%v", gateway.createCount(), gateway.isEnabled(), gateway.groupRatio(relation.GroupKey), gateway.groupVisible(relation.GroupKey))
	}

	repeated, err := synchronizationService.RequestSync(ctx, relation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ID != operation.ID || gateway.createCount() != 1 {
		t.Fatalf("repeated synchronization was not idempotent: first=%s repeated=%s create=%d", operation.ID, repeated.ID, gateway.createCount())
	}

	stopContext, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := riverClient.Stop(stopContext); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()
	stopped = true
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"model-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer user-api-key")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("gateway call failed after worker stopped: status=%d body=%s", response.StatusCode, body)
	}
}

const (
	fakeNewAPIAdminToken = "new-api-admin-token"
	fakeSupplierKey      = "supplier-secret-key"
)

type fakeNewAPI struct {
	mutex      sync.Mutex
	ratios     map[string]string
	userGroups map[string]string
	channel    map[string]any
	created    int
}

func newFakeNewAPI() *fakeNewAPI {
	return &fakeNewAPI{
		ratios:     map[string]string{"default": "1"},
		userGroups: map[string]string{"default": "Default"},
	}
}

func (f *fakeNewAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(request.URL.Path, "/api/") && request.URL.Path != "/api/status" && request.Header.Get("Authorization") != "Bearer "+fakeNewAPIAdminToken {
		writer.WriteHeader(http.StatusUnauthorized)
		f.write(writer, map[string]any{"success": false, "message": "unauthorized"})
		return
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/status":
		f.write(writer, map[string]any{"success": true, "message": "", "data": map[string]any{"version": "0ed497f0"}})
	case request.Method == http.MethodGet && request.URL.Path == "/api/option/":
		userGroups, _ := json.Marshal(f.userGroups)
		f.write(writer, map[string]any{"success": true, "message": "", "data": []map[string]string{
			{"key": "GroupRatio", "value": encodeFakeRatios(f.ratios)},
			{"key": "UserUsableGroups", "value": string(userGroups)},
		}})
	case request.Method == http.MethodPut && request.URL.Path == "/api/option/":
		var payload struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if !f.decode(writer, request, &payload) {
			return
		}
		switch payload.Key {
		case "GroupRatio":
			decodedRatios, err := decodeFakeRatios(payload.Value)
			if err != nil {
				f.write(writer, map[string]any{"success": false, "message": "invalid ratios"})
				return
			}
			f.ratios = decodedRatios
		case "UserUsableGroups":
			decodedGroups := make(map[string]string)
			if err := json.Unmarshal([]byte(payload.Value), &decodedGroups); err != nil {
				f.write(writer, map[string]any{"success": false, "message": "invalid user groups"})
				return
			}
			f.userGroups = decodedGroups
		default:
			f.write(writer, map[string]any{"success": false, "message": "invalid ratios"})
			return
		}
		f.write(writer, map[string]any{"success": true, "message": ""})
	case request.Method == http.MethodGet && request.URL.Path == "/api/channel/":
		items := []map[string]any{}
		if f.channel != nil {
			items = append(items, f.channel)
		}
		f.write(writer, map[string]any{"success": true, "message": "", "data": map[string]any{"items": items, "total": len(items), "page": 1, "page_size": 100}})
	case request.Method == http.MethodPost && request.URL.Path == "/api/channel/":
		var payload struct {
			Mode    string         `json:"mode"`
			Channel map[string]any `json:"channel"`
		}
		if !f.decode(writer, request, &payload) {
			return
		}
		if payload.Mode != "single" || payload.Channel["key"] != fakeSupplierKey {
			f.write(writer, map[string]any{"success": false, "message": "invalid channel"})
			return
		}
		f.created++
		payload.Channel["id"] = 42
		f.channel = payload.Channel
		f.write(writer, map[string]any{"success": true, "message": ""})
	case request.Method == http.MethodGet && request.URL.Path == "/api/channel/test/42":
		f.write(writer, map[string]any{"success": true, "message": "", "time": 0.01})
	case request.Method == http.MethodPost && request.URL.Path == "/api/channel/42/status":
		var payload struct {
			Status int `json:"status"`
		}
		if !f.decode(writer, request, &payload) {
			return
		}
		if f.channel == nil {
			f.write(writer, map[string]any{"success": false, "message": "missing channel"})
			return
		}
		f.channel["status"] = payload.Status
		f.write(writer, map[string]any{"success": true, "message": "", "data": true})
	case request.Method == http.MethodPost && request.URL.Path == "/v1/chat/completions":
		if !f.isEnabledLocked() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			f.write(writer, map[string]any{"error": "no enabled channel"})
			return
		}
		f.write(writer, map[string]any{"id": "completion-test", "model": "model-a"})
	default:
		writer.WriteHeader(http.StatusNotFound)
		f.write(writer, map[string]any{"success": false, "message": fmt.Sprintf("unhandled %s %s", request.Method, request.URL.Path)})
	}
}

func (f *fakeNewAPI) decode(writer http.ResponseWriter, request *http.Request, target any) bool {
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		f.write(writer, map[string]any{"success": false, "message": "invalid JSON"})
		return false
	}
	return true
}

func (f *fakeNewAPI) write(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}

func (f *fakeNewAPI) createCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.created
}

func (f *fakeNewAPI) isEnabled() bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.isEnabledLocked()
}

func (f *fakeNewAPI) isEnabledLocked() bool {
	if f.channel == nil {
		return false
	}
	groupAvailable := false
	for key := range f.userGroups {
		if strings.HasPrefix(key, "mr_s_") {
			_, groupAvailable = f.ratios[key]
			if groupAvailable {
				break
			}
		}
	}
	if !groupAvailable {
		return false
	}
	status, ok := f.channel["status"].(float64)
	if ok {
		return status == 1
	}
	statusInt, ok := f.channel["status"].(int)
	return ok && statusInt == 1
}

func (f *fakeNewAPI) groupRatio(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.ratios[key]
}

func (f *fakeNewAPI) groupVisible(key string) bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	_, exists := f.userGroups[key]
	return exists
}

func encodeFakeRatios(ratios map[string]string) string {
	values := make(map[string]json.RawMessage, len(ratios))
	for key, ratio := range ratios {
		values[key] = json.RawMessage(ratio)
	}
	payload, _ := json.Marshal(values)
	return string(payload)
}

func decodeFakeRatios(value string) (map[string]string, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var raw map[string]json.Number
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(raw))
	for key, number := range raw {
		ratio, err := decimal.NewFromString(number.String())
		if err != nil {
			return nil, err
		}
		result[key] = ratio.String()
	}
	return result, nil
}
