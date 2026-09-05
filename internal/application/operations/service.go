package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

type Store interface {
	ListOperations(context.Context, string, domain.Filter) (domain.Page, error)
	GetOperationResource(context.Context, string, uuid.UUID) (json.RawMessage, error)
	MutateOperations(context.Context, domain.Mutation) (json.RawMessage, error)
	GetSiteAccess(context.Context, uuid.UUID) (domain.SiteAccess, error)
	GetSupplierAccess(context.Context, uuid.UUID) (domain.SupplierAccess, error)
	GetOperationReplay(context.Context, domain.Mutation) (json.RawMessage, bool, error)
}

type Vault interface {
	Encrypt(uuid.UUID, credential.Purpose, []byte) (credential.Record, error)
	Decrypt(credential.Record) ([]byte, error)
}

type SupplierCredentialChecker interface {
	Check(context.Context, string, []byte) error
}

type Service struct {
	store               Store
	vault               Vault
	gateways            reconciliation.SiteGatewayFactory
	supplierCredentials SupplierCredentialChecker
}

func NewService(store Store, vault Vault, gateways reconciliation.SiteGatewayFactory, supplierCredentials SupplierCredentialChecker) (*Service, error) {
	if store == nil || vault == nil || gateways == nil || supplierCredentials == nil {
		return nil, errors.New("operations dependencies are required")
	}
	return &Service{store: store, vault: vault, gateways: gateways, supplierCredentials: supplierCredentials}, nil
}

func (s *Service) List(ctx context.Context, kind string, filter domain.Filter) (domain.Page, error) {
	filter, err := domain.NormalizeFilter(filter)
	if err != nil {
		return domain.Page{}, err
	}
	return s.store.ListOperations(ctx, kind, filter)
}

func (s *Service) Get(ctx context.Context, kind string, id uuid.UUID) (json.RawMessage, error) {
	return s.store.GetOperationResource(ctx, kind, id)
}

func (s *Service) Execute(ctx context.Context, mutation domain.Mutation) (json.RawMessage, error) {
	if mutation.Actor == "" || len(mutation.Key) < 8 || len(mutation.Key) > 128 {
		return nil, fmt.Errorf("%w: 操作身份或请求编号无效", domain.ErrInvalid)
	}
	payload, err := json.Marshal(mutation.Input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	mutation.RequestHash = hex.EncodeToString(digest[:])
	if raw, found, err := s.store.GetOperationReplay(ctx, mutation); err != nil || found {
		return raw, err
	}
	if err = s.prepare(ctx, &mutation); err != nil {
		if raw, found, replayErr := s.store.GetOperationReplay(ctx, mutation); found || replayErr != nil {
			return raw, replayErr
		}
		return nil, err
	}
	return s.store.MutateOperations(ctx, mutation)
}

func (s *Service) readBasis(ctx context.Context, id uuid.UUID) (domain.BillingBasis, error) {
	access, err := s.store.GetSiteAccess(ctx, id)
	if err != nil {
		return domain.BillingBasis{}, err
	}
	token, err := s.vault.Decrypt(access.Credential)
	if err != nil {
		return domain.BillingBasis{}, err
	}
	defer clear(token)
	gateway, err := s.gateways.NewForSite(access.BaseURL, token, access.AdminUserID)
	if err != nil {
		return domain.BillingBasis{}, err
	}
	reader, ok := gateway.(reconciliation.BillingBasisReader)
	if !ok {
		return domain.BillingBasis{}, fmt.Errorf("%w: 站点无法读取计费基准", domain.ErrInvalid)
	}
	values, hash, err := reader.ReadBillingBasis(ctx)
	if err != nil {
		return domain.BillingBasis{}, err
	}
	return domain.BillingBasis{Values: values, Hash: hash}, nil
}

func (s *Service) seal(mutation *domain.Mutation, purpose credential.Purpose, secret string) error {
	if len(secret) < 8 || len(secret) > 16384 {
		return fmt.Errorf("%w: 访问凭证长度无效", domain.ErrInvalid)
	}
	record, err := s.vault.Encrypt(uuid.New(), purpose, []byte(secret))
	if err != nil {
		return err
	}
	mutation.Sealed = &record
	return nil
}
