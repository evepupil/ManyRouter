package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const maxCredentialLength = 8192

var ErrInvalidInput = errors.New("invalid onboarding input")

type Store interface {
	CreateSite(context.Context, site.Site, credential.Record, string) error
	GetSite(context.Context, uuid.UUID) (site.Site, error)
	CreateSupplier(context.Context, supplier.Supplier, credential.Record, string) error
	GetSupplier(context.Context, uuid.UUID) (supplier.Supplier, error)
	CreateRelationAndPlan(
		context.Context,
		routing.Relation,
		routing.ManagedChannel,
		uuid.UUID,
		routing.Snapshot,
		[]byte,
		string,
		string,
		string,
	) (routing.Relation, routing.Plan, error)
	GetRelation(context.Context, uuid.UUID) (routing.Relation, error)
}

type CredentialSealer interface {
	Encrypt(uuid.UUID, credential.Purpose, []byte) (credential.Record, error)
}

type Service struct {
	store  Store
	sealer CredentialSealer
	now    func() time.Time
	newID  func() uuid.UUID
}

type CreateSiteCommand struct {
	Code              string
	Name              string
	NewAPIBaseURL     string
	NewAPIAccessToken string
	ActorID           string
}

type CreateSupplierCommand struct {
	Code            string
	Name            string
	UpstreamBaseURL string
	UpstreamAPIKey  string
	Models          []supplier.ModelInput
	ActorID         string
}

type CreateRelationCommand struct {
	SiteID           uuid.UUID
	SupplierID       uuid.UUID
	GroupDisplayName string
	SaleRatio        decimal.Decimal
	Visible          bool
	ActorID          string
}

func NewService(store Store, sealer CredentialSealer, now func() time.Time, newID func() uuid.UUID) (*Service, error) {
	if store == nil || sealer == nil || now == nil || newID == nil {
		return nil, errors.New("onboarding dependencies are required")
	}
	return &Service{store: store, sealer: sealer, now: now, newID: newID}, nil
}

func (s *Service) CreateSite(ctx context.Context, command CreateSiteCommand) (site.Site, error) {
	secret, err := validateCredential(command.NewAPIAccessToken)
	if err != nil {
		return site.Site{}, invalidInput("new API access token", err)
	}
	defer clear(secret)

	now := s.now().UTC()
	credentialID := s.newID()
	siteData, err := site.New(s.newID(), command.Code, command.Name, command.NewAPIBaseURL, credentialID, now)
	if err != nil {
		return site.Site{}, invalidInput("site", err)
	}
	sealed, err := s.sealer.Encrypt(credentialID, credential.PurposeNewAPIAdmin, secret)
	if err != nil {
		return site.Site{}, fmt.Errorf("seal New API access token: %w", err)
	}
	if err := s.store.CreateSite(ctx, siteData, sealed, normalizeActor(command.ActorID)); err != nil {
		return site.Site{}, fmt.Errorf("store site: %w", err)
	}
	return siteData, nil
}

func (s *Service) CreateSupplier(ctx context.Context, command CreateSupplierCommand) (supplier.Supplier, error) {
	secret, err := validateCredential(command.UpstreamAPIKey)
	if err != nil {
		return supplier.Supplier{}, invalidInput("upstream API key", err)
	}
	defer clear(secret)

	now := s.now().UTC()
	credentialID := s.newID()
	supplierData, err := supplier.New(
		s.newID(),
		command.Code,
		command.Name,
		command.UpstreamBaseURL,
		credentialID,
		command.Models,
		now,
	)
	if err != nil {
		return supplier.Supplier{}, invalidInput("supplier", err)
	}
	sealed, err := s.sealer.Encrypt(credentialID, credential.PurposeSupplierAPIKey, secret)
	if err != nil {
		return supplier.Supplier{}, fmt.Errorf("seal supplier API key: %w", err)
	}
	if err := s.store.CreateSupplier(ctx, supplierData, sealed, normalizeActor(command.ActorID)); err != nil {
		return supplier.Supplier{}, fmt.Errorf("store supplier: %w", err)
	}
	return supplierData, nil
}

func (s *Service) CreateRelation(ctx context.Context, command CreateRelationCommand) (routing.Relation, routing.Plan, error) {
	siteData, err := s.store.GetSite(ctx, command.SiteID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("get site: %w", err)
	}
	supplierData, err := s.store.GetSupplier(ctx, command.SupplierID)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("get supplier: %w", err)
	}
	if err := siteData.CanSync(); err != nil {
		return routing.Relation{}, routing.Plan{}, invalidInput("site", err)
	}
	if err := supplierData.CanDeploy(); err != nil {
		return routing.Relation{}, routing.Plan{}, invalidInput("supplier", err)
	}

	now := s.now().UTC()
	relation, err := routing.NewRelation(
		s.newID(),
		command.SiteID,
		command.SupplierID,
		command.GroupDisplayName,
		command.SaleRatio,
		command.Visible,
		now,
	)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, invalidInput("site supplier", err)
	}
	channel, err := routing.NewManagedChannel(s.newID(), relation.ID, now)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, invalidInput("managed channel", err)
	}
	snapshot, err := routing.BuildSnapshot(siteData, supplierData, relation, channel)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, invalidInput("route plan", err)
	}
	payload, contentHash, err := routing.EncodeSnapshot(snapshot)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("encode route plan: %w", err)
	}

	storedRelation, plan, err := s.store.CreateRelationAndPlan(
		ctx,
		relation,
		channel,
		s.newID(),
		snapshot,
		payload,
		contentHash,
		"supplier_onboarded",
		normalizeActor(command.ActorID),
	)
	if err != nil {
		return routing.Relation{}, routing.Plan{}, fmt.Errorf("store site supplier relation: %w", err)
	}
	return storedRelation, plan, nil
}

func (s *Service) GetSite(ctx context.Context, id uuid.UUID) (site.Site, error) {
	return s.store.GetSite(ctx, id)
}

func (s *Service) GetSupplier(ctx context.Context, id uuid.UUID) (supplier.Supplier, error) {
	return s.store.GetSupplier(ctx, id)
}

func (s *Service) GetRelation(ctx context.Context, id uuid.UUID) (routing.Relation, error) {
	return s.store.GetRelation(ctx, id)
}

func validateCredential(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("value is required")
	}
	if len(trimmed) > maxCredentialLength {
		return nil, fmt.Errorf("value must not exceed %d bytes", maxCredentialLength)
	}
	return []byte(trimmed), nil
}

func normalizeActor(actorID string) string {
	if actor := strings.TrimSpace(actorID); actor != "" {
		return actor
	}
	return "m0-owner"
}

func invalidInput(field string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidInput, field, err)
}
