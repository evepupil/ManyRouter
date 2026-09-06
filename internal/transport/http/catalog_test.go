package httptransport_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	catalogapp "github.com/evepupil/ManyRouter/internal/application/catalog"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	httptransport "github.com/evepupil/ManyRouter/internal/transport/http"
	"github.com/google/uuid"
)

type fakeCatalog struct {
	token    string
	snapshot domaincatalog.Snapshot
}

func (catalog *fakeCatalog) CreateToken(context.Context, uuid.UUID, string, string) (catalogapp.IssuedToken, error) {
	return catalogapp.IssuedToken{}, nil
}

func (catalog *fakeCatalog) ListTokens(context.Context, uuid.UUID) ([]catalogapp.TokenRecord, error) {
	return nil, nil
}

func (catalog *fakeCatalog) RevokeToken(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	return nil
}

func (catalog *fakeCatalog) Authenticate(_ context.Context, token string) (uuid.UUID, error) {
	if token != catalog.token {
		return uuid.Nil, catalogapp.ErrUnauthorized
	}
	return catalog.snapshot.SiteID, nil
}

func (catalog *fakeCatalog) GetProducts(_ context.Context, token string) (domaincatalog.Snapshot, error) {
	if token != catalog.token {
		return domaincatalog.Snapshot{}, catalogapp.ErrUnauthorized
	}
	return catalog.snapshot, nil
}

func TestSiteProductRouteUsesScopedTokenWithoutOperatorAccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	siteID := uuid.New()
	token := "mrp_" + strings.Repeat("a", 43)
	catalog := &fakeCatalog{token: token, snapshot: domaincatalog.Snapshot{
		ID: uuid.New(), ContractVersion: domaincatalog.ContractVersion, Version: 1,
		SiteID: siteID, SiteName: "站点", RoutePlanID: uuid.New(), GeneratedAt: now,
		Products: []domaincatalog.Product{}, ContentHash: strings.Repeat("b", 64),
	}}
	idempotencyService, err := idempotency.NewService(&memoryIdempotencyStore{}, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := httptransport.NewHandler(&fakeOnboarding{}, fakeReconciliation{}, idempotencyService, logger, httptransport.WithCatalog(catalog))
	if err != nil {
		t.Fatal(err)
	}
	router, err := httptransport.NewRouter(handler, strings.Repeat("o", 32), logger)
	if err != nil {
		t.Fatal(err)
	}
	httptransport.RegisterCatalogRoutes(router, handler)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/site/products", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" || strings.Contains(response.Body.String(), token) {
		t.Fatalf("site product response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/ops/site-product-tokens?site_id="+siteID.String(), nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("site token management was public: %d", unauthorized.Code)
	}
	bad := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodGet, "/api/v1/site/products", nil)
	badRequest.Header.Set("Authorization", "Bearer wrong")
	router.ServeHTTP(bad, badRequest)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad site token returned %d", bad.Code)
	}
}
