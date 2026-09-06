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
	"github.com/evepupil/ManyRouter/internal/application/runtimehealth"
	httptransport "github.com/evepupil/ManyRouter/internal/transport/http"
	"github.com/google/uuid"
)

type fakeRuntimeHealth struct {
	checkCalls int
	site       runtimehealth.SiteSnapshot
}

func (fake *fakeRuntimeHealth) Summary(context.Context) (runtimehealth.Snapshot, error) {
	return runtimehealth.Snapshot{
		Status: runtimehealth.LevelNormal, GeneratedAt: time.Now().UTC(),
		System: runtimehealth.SystemSnapshot{Status: runtimehealth.LevelNormal},
		Sites:  []runtimehealth.SiteSnapshot{fake.site},
	}, nil
}

func (fake *fakeRuntimeHealth) Detail(context.Context, uuid.UUID) (runtimehealth.SiteSnapshot, error) {
	return fake.site, nil
}

func (fake *fakeRuntimeHealth) Check(context.Context, uuid.UUID, string) (runtimehealth.SiteSnapshot, error) {
	fake.checkCalls++
	return fake.site, nil
}

func (fake *fakeRuntimeHealth) Prometheus(context.Context) (string, error) {
	return "manyrouter_database_up 1\n", nil
}

func TestRuntimeHealthRoutesRequireAuthAndReplayChecks(t *testing.T) {
	now := time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC)
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	onboardingService := &fakeOnboarding{}
	records := &memoryIdempotencyStore{}
	idempotencyService, err := idempotency.NewService(records, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	runtimeService := &fakeRuntimeHealth{site: runtimehealth.SiteSnapshot{
		SiteFacts: runtimehealth.SiteFacts{SiteID: siteID, SiteCode: "site-a", SiteName: "Site A", SiteStatus: "enabled"},
		Status:    runtimehealth.LevelNormal, Reasons: []runtimehealth.Reason{},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := httptransport.NewHandler(
		onboardingService, fakeReconciliation{}, idempotencyService, logger,
		httptransport.WithRuntimeHealth(runtimeService),
	)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken := strings.Repeat("t", 32)
	router, err := httptransport.NewRouter(handler, operatorToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	httptransport.RegisterRuntimeHealthRoutes(router, handler)

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/ops/runtime-health", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/ops/runtime-health/"+siteID.String()+"/check", bytes.NewBufferString(`{}`))
		request.Header.Set("Authorization", "Bearer "+operatorToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "runtime-check-1")
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("check status = %d body=%s", response.Code, response.Body.String())
		}
	}
	if runtimeService.checkCalls != 1 {
		t.Fatalf("runtime check calls = %d", runtimeService.checkCalls)
	}

	metrics := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRequest.Header.Set("Authorization", "Bearer "+operatorToken)
	router.ServeHTTP(metrics, metricsRequest)
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "manyrouter_database_up 1") {
		t.Fatalf("metrics status = %d body=%s", metrics.Code, metrics.Body.String())
	}
}
