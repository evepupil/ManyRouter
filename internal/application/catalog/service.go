package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/google/uuid"
)

var (
	ErrInvalid      = errors.New("invalid catalog request")
	ErrUnauthorized = errors.New("catalog token is unauthorized")
)

const productTokenPrefix = "mrp_"

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
	newID      func() uuid.UUID
}

func NewService(repository Repository, random io.Reader, now func() time.Time, newID func() uuid.UUID) (*Service, error) {
	if repository == nil || random == nil || now == nil || newID == nil {
		return nil, errors.New("catalog dependencies are required")
	}
	return &Service{repository: repository, random: random, now: now, newID: newID}, nil
}

func (service *Service) CreateToken(ctx context.Context, siteID uuid.UUID, reason, actor string) (IssuedToken, error) {
	reason = strings.TrimSpace(reason)
	actor = strings.TrimSpace(actor)
	if siteID == uuid.Nil || len(reason) < 3 || len(reason) > 500 || actor == "" {
		return IssuedToken{}, fmt.Errorf("%w: 站点、原因或操作身份无效", ErrInvalid)
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(service.random, raw); err != nil {
		return IssuedToken{}, err
	}
	defer clear(raw)
	token := productTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	record := TokenRecord{
		ID: service.newID(), SiteID: siteID, TokenHash: productTokenHash(token), Status: "active",
		Reason: reason, CreatedBy: actor, CreatedAt: service.now().UTC(),
	}
	if err := service.repository.CreateProductToken(ctx, record); err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{TokenRecord: record, Token: token}, nil
}

func (service *Service) ListTokens(ctx context.Context, siteID uuid.UUID) ([]TokenRecord, error) {
	if siteID == uuid.Nil {
		return nil, fmt.Errorf("%w: 站点不能为空", ErrInvalid)
	}
	return service.repository.ListProductTokens(ctx, siteID)
}

func (service *Service) RevokeToken(ctx context.Context, siteID, tokenID uuid.UUID, reason, actor string) error {
	reason = strings.TrimSpace(reason)
	actor = strings.TrimSpace(actor)
	if siteID == uuid.Nil || tokenID == uuid.Nil || len(reason) < 3 || len(reason) > 500 || actor == "" {
		return fmt.Errorf("%w: 站点、令牌、原因或操作身份无效", ErrInvalid)
	}
	return service.repository.RevokeProductToken(ctx, siteID, tokenID, reason, actor, service.now().UTC())
}

func (service *Service) GetProducts(ctx context.Context, token string) (domaincatalog.Snapshot, error) {
	siteID, err := service.Authenticate(ctx, token)
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	return service.RefreshSite(ctx, siteID)
}

func (service *Service) Authenticate(ctx context.Context, token string) (uuid.UUID, error) {
	if !validProductToken(token) {
		return uuid.Nil, ErrUnauthorized
	}
	return service.repository.AuthenticateProductToken(ctx, productTokenHash(token), service.now().UTC())
}

func (service *Service) RefreshSite(ctx context.Context, siteID uuid.UUID) (domaincatalog.Snapshot, error) {
	if siteID == uuid.Nil {
		return domaincatalog.Snapshot{}, fmt.Errorf("%w: 站点不能为空", ErrInvalid)
	}
	source, err := service.repository.LoadCatalogSource(ctx, siteID)
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	source.Now = service.now().UTC()
	snapshot, err := domaincatalog.Build(source)
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	snapshot.ID = service.newID()
	hash, err := domaincatalog.ContentHash(snapshot)
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	snapshot.ContentHash = hash
	return service.repository.SaveProductSnapshot(ctx, snapshot, hash)
}

func validProductToken(token string) bool {
	if !strings.HasPrefix(token, productTokenPrefix) || len(token) != len(productTokenPrefix)+43 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, productTokenPrefix))
	return err == nil && len(raw) == 32
}

func productTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
