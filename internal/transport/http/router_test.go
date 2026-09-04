package httptransport_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	httptransport "github.com/evepupil/ManyRouter/internal/transport/http"
	"github.com/google/uuid"
)

type fakeOnboarding struct {
	createSiteCalls int
	createdSite     site.Site
}

func (f *fakeOnboarding) CreateSite(context.Context, onboarding.CreateSiteCommand) (site.Site, error) {
	f.createSiteCalls++
	return f.createdSite, nil
}

func (f *fakeOnboarding) GetSite(context.Context, uuid.UUID) (site.Site, error) {
	return f.createdSite, nil
}

func (f *fakeOnboarding) CreateSupplier(context.Context, onboarding.CreateSupplierCommand) (supplier.Supplier, error) {
	return supplier.Supplier{}, nil
}

func (f *fakeOnboarding) GetSupplier(context.Context, uuid.UUID) (supplier.Supplier, error) {
	return supplier.Supplier{}, nil
}

func (f *fakeOnboarding) CreateRelation(context.Context, onboarding.CreateRelationCommand) (routing.Relation, routing.Plan, error) {
	return routing.Relation{}, routing.Plan{}, nil
}

func (f *fakeOnboarding) GetRelation(context.Context, uuid.UUID) (routing.Relation, error) {
	return routing.Relation{}, nil
}

type fakeReconciliation struct{}

func (fakeReconciliation) RequestSync(context.Context, uuid.UUID) (reconciliation.Operation, error) {
	return reconciliation.Operation{}, nil
}

func (fakeReconciliation) GetOperation(context.Context, uuid.UUID) (reconciliation.Operation, error) {
	return reconciliation.Operation{}, nil
}

type memoryIdempotencyStore struct {
	record *idempotency.Record
}

func (s *memoryIdempotencyStore) FindIdempotencyRecord(_ context.Context, scope, key string, now time.Time) (*idempotency.Record, error) {
	if s.record == nil || s.record.Scope != scope || s.record.Key != key || !s.record.ExpiresAt.After(now) {
		return nil, nil
	}
	return s.record, nil
}

func (s *memoryIdempotencyStore) SaveIdempotencyRecord(_ context.Context, record idempotency.Record) error {
	s.record = &record
	return nil
}

func TestRouterProtectsManagementAndReplaysCreateResponse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	onboardingService := &fakeOnboarding{createdSite: site.Site{
		ID: uuid.MustParse("10000000-0000-0000-0000-000000000001"), Code: "site-a", Name: "Site A",
		NewAPIBaseURL: "https://gateway.example", Status: site.StatusEnabled,
		CompatibilityStatus: site.CompatibilityUnknown, Version: 1, CreatedAt: now, UpdatedAt: now,
	}}
	idempotencyService, err := idempotency.NewService(&memoryIdempotencyStore{}, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := httptransport.NewHandler(onboardingService, fakeReconciliation{}, idempotencyService, logger)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken := strings.Repeat("t", 32)
	router, err := httptransport.NewRouter(handler, operatorToken, logger)
	if err != nil {
		t.Fatal(err)
	}

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))
	if health.Code != http.StatusOK || health.Header().Get("X-Request-ID") == "" {
		t.Fatalf("health response = %d headers=%v body=%s", health.Code, health.Header(), health.Body.String())
	}

	body := `{"code":"site-a","name":"Site A","new_api_base_url":"https://gateway.example","new_api_access_token":"top-secret"}`
	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewBufferString(body))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	unauthorizedRequest.Header.Set("Idempotency-Key", "request-123")
	router.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized || onboardingService.createSiteCalls != 0 {
		t.Fatalf("unauthorized request reached use case: code=%d calls=%d", unauthorized.Code, onboardingService.createSiteCalls)
	}

	var firstBody string
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/sites", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+operatorToken)
		request.Header.Set("Idempotency-Key", "request-123")
		router.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create response = %d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "top-secret") {
			t.Fatalf("credential leaked in response: %s", response.Body.String())
		}
		if attempt == 0 {
			firstBody = response.Body.String()
		} else if response.Body.String() != firstBody {
			t.Fatalf("idempotent response changed:\n%s\n%s", firstBody, response.Body.String())
		}
	}
	if onboardingService.createSiteCalls != 1 {
		t.Fatalf("idempotent replay called use case %d times", onboardingService.createSiteCalls)
	}
}
