package catalog_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	catalogapp "github.com/evepupil/ManyRouter/internal/application/catalog"
	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

type fakeRepository struct {
	token      catalogapp.TokenRecord
	authSiteID uuid.UUID
	source     domaincatalog.BuildInput
	saved      domaincatalog.Snapshot
	revokedID  uuid.UUID
}

func (repository *fakeRepository) CreateProductToken(_ context.Context, record catalogapp.TokenRecord) error {
	repository.token = record
	return nil
}

func (repository *fakeRepository) ListProductTokens(context.Context, uuid.UUID) ([]catalogapp.TokenRecord, error) {
	return []catalogapp.TokenRecord{repository.token}, nil
}

func (repository *fakeRepository) RevokeProductToken(_ context.Context, _ uuid.UUID, tokenID uuid.UUID, _, _ string, _ time.Time) error {
	repository.revokedID = tokenID
	return nil
}

func (repository *fakeRepository) AuthenticateProductToken(context.Context, string, time.Time) (uuid.UUID, error) {
	if repository.authSiteID == uuid.Nil {
		return uuid.Nil, catalogapp.ErrUnauthorized
	}
	return repository.authSiteID, nil
}

func (repository *fakeRepository) LoadCatalogSource(context.Context, uuid.UUID) (domaincatalog.BuildInput, error) {
	return repository.source, nil
}

func (repository *fakeRepository) SaveProductSnapshot(_ context.Context, snapshot domaincatalog.Snapshot, hash string) (domaincatalog.Snapshot, error) {
	snapshot.Version = 1
	snapshot.ContentHash = hash
	repository.saved = snapshot
	return snapshot, nil
}

func TestCreateTokenReturnsSecretOnceAndStoresOnlyHash(t *testing.T) {
	t.Parallel()
	repository := &fakeRepository{}
	service := newCatalogService(t, repository)
	issued, err := service.CreateToken(context.Background(), uuid.New(), "接入用户页面", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.Token) != 47 || issued.TokenHash == "" || issued.TokenHash == issued.Token {
		t.Fatalf("unexpected issued token: %#v", issued)
	}
	if repository.token.TokenHash != issued.TokenHash || repository.token.TokenHash == issued.Token {
		t.Fatal("repository must receive only the token hash")
	}
}

func TestGetProductsRequiresScopedTokenAndBuildsSnapshot(t *testing.T) {
	t.Parallel()
	siteID := uuid.New()
	repository := &fakeRepository{
		authSiteID: siteID,
		source: domaincatalog.BuildInput{
			SiteID: siteID, SiteName: "站点", RoutePlanID: uuid.New(),
			Plan: routing.Snapshot{SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: siteID},
		},
	}
	service := newCatalogService(t, repository)
	if _, err := service.GetProducts(context.Background(), "bad"); !errors.Is(err, catalogapp.ErrUnauthorized) {
		t.Fatalf("invalid token should be rejected: %v", err)
	}
	issued, err := service.CreateToken(context.Background(), siteID, "接入用户页面", "operator")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.GetProducts(context.Background(), issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SiteID != siteID || snapshot.Version != 1 || snapshot.ContentHash == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func newCatalogService(t *testing.T, repository *fakeRepository) *catalogapp.Service {
	t.Helper()
	service, err := catalogapp.NewService(
		repository,
		bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
		func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
		uuid.New,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
