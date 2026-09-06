package compatibility

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

type SiteAccess struct {
	ID          uuid.UUID
	Code        string
	Name        string
	BaseURL     string
	AdminUserID int64
	Credential  credential.Record
}

type Store interface {
	GetCompatibilitySite(context.Context, uuid.UUID) (SiteAccess, error)
	ListCompatibilitySiteIDs(context.Context) ([]uuid.UUID, error)
	SaveCompatibilityCheck(context.Context, Report, site.CompatibilityStatus) error
	GetLatestCompatibilityCheck(context.Context, uuid.UUID) (Report, error)
	ListLatestCompatibilityChecks(context.Context) ([]Report, error)
}

type Vault interface {
	Decrypt(credential.Record) ([]byte, error)
}

type Service struct {
	store    Store
	vault    Vault
	gateways reconciliation.SiteGatewayFactory
	manifest Manifest
	now      func() time.Time
	newID    func() uuid.UUID
}

func NewService(
	store Store,
	vault Vault,
	gateways reconciliation.SiteGatewayFactory,
	manifest Manifest,
	now func() time.Time,
	newID func() uuid.UUID,
) (*Service, error) {
	if store == nil || vault == nil || gateways == nil || now == nil || newID == nil {
		return nil, errors.New("compatibility dependencies are required")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &Service{store: store, vault: vault, gateways: gateways, manifest: manifest, now: now, newID: newID}, nil
}

func (service *Service) CheckSite(ctx context.Context, siteID uuid.UUID, actor string) (Report, error) {
	access, err := service.store.GetCompatibilitySite(ctx, siteID)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		ID: service.newID(), SiteID: access.ID, SiteCode: access.Code, SiteName: access.Name,
		Mode: ModeUnknown, Verdict: VerdictUnreachable, CatalogVersion: service.manifest.Version,
		Conflicts: make([]string, 0), Reasons: make([]Reason, 0), CheckedBy: actor, CheckedAt: service.now().UTC(),
	}
	secret, err := service.vault.Decrypt(access.Credential)
	if err != nil {
		report.ErrorCode = "credential_unavailable"
		report.ErrorMessage = "站点访问凭证无法解密。"
		report.Reasons = append(report.Reasons, Reason{Code: report.ErrorCode, Message: report.ErrorMessage, Action: "更新站点访问凭证后重新检查。"})
		return service.save(ctx, report)
	}
	defer clear(secret)
	gateway, err := service.gateways.NewForSite(access.BaseURL, secret, access.AdminUserID)
	if err != nil {
		report.ErrorCode = "gateway_configuration"
		report.ErrorMessage = "站点地址或访问配置无效。"
		report.Reasons = append(report.Reasons, Reason{Code: report.ErrorCode, Message: report.ErrorMessage, Action: "检查站点地址和凭证后重新检查。"})
		return service.save(ctx, report)
	}
	managed, supported := gateway.(reconciliation.ManagedSyncGateway)
	if supported {
		capability, capabilityErr := managed.ReadManagedSyncCapabilities(ctx)
		if capabilityErr == nil {
			return service.checkManaged(ctx, report, managed, capability)
		}
		if !legacyFallback(capabilityErr) {
			return service.saveGatewayFailure(ctx, report, capabilityErr)
		}
	}
	actual, err := gateway.ReadActualState(ctx)
	if err != nil {
		return service.saveGatewayFailure(ctx, report, err)
	}
	report.Mode = ModeLegacy
	report.NewAPIVersion = actual.Version
	report.Verdict, report.Reasons = service.manifest.EvaluateLegacy(actual.Version)
	return service.save(ctx, report)
}

func (service *Service) checkManaged(
	ctx context.Context,
	report Report,
	gateway reconciliation.ManagedSyncGateway,
	capability reconciliation.ManagedSyncCapabilities,
) (Report, error) {
	report.Mode = ModeManaged
	report.NewAPIVersion = capability.NewAPIVersion
	report.ContractVersion = capability.ContractVersion
	report.DatabaseType = capability.DatabaseType
	report.Capabilities = reportCapabilities(capability)
	report.BillingBasisHash = capability.BillingBasisHash
	stateValue, err := gateway.ReadManagedState(ctx)
	if err != nil {
		return service.saveGatewayFailure(ctx, report, err)
	}
	report.StateHash = stateValue.StateHash
	report.BillingBasisHash = stateValue.BillingBasisHash
	report.Conflicts = append(report.Conflicts, stateValue.Conflicts...)
	report.Verdict, report.Reasons = service.manifest.EvaluateManaged(capability, stateValue)
	return service.save(ctx, report)
}

func (service *Service) saveGatewayFailure(ctx context.Context, report Report, err error) (Report, error) {
	report.Verdict = VerdictUnreachable
	report.ErrorCode = "site_unreachable"
	report.ErrorMessage = "无法完成站点兼容检查。"
	action := "检查 New API 服务、网络和站点凭证后重试。"
	var failure *reconciliation.Failure
	if errors.As(err, &failure) {
		report.ErrorCode = failure.Code
		if failure.Kind == reconciliation.FailureAuthentication || failure.Kind == reconciliation.FailureCompatibility {
			report.Verdict = VerdictIncompatible
		}
		if failure.Kind == reconciliation.FailureAuthentication {
			report.ErrorMessage = "站点拒绝了当前访问凭证。"
			action = "更新站点访问凭证后重新检查。"
		}
	}
	report.Reasons = append(report.Reasons, Reason{Code: report.ErrorCode, Message: report.ErrorMessage, Action: action})
	return service.save(ctx, report)
}

func (service *Service) save(ctx context.Context, report Report) (Report, error) {
	if report.CheckedBy == "" {
		report.CheckedBy = "system:compatibility"
	}
	if err := service.store.SaveCompatibilityCheck(ctx, report, siteCompatibilityStatus(report.Verdict)); err != nil {
		return Report{}, fmt.Errorf("save compatibility check: %w", err)
	}
	return report, nil
}

func (service *Service) CheckAll(ctx context.Context) error {
	siteIDs, err := service.store.ListCompatibilitySiteIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, siteID := range siteIDs {
		if _, err := service.CheckSite(ctx, siteID, "system:compatibility"); err != nil {
			failures = append(failures, fmt.Errorf("check site %s: %w", siteID, err))
		}
	}
	return errors.Join(failures...)
}

func (service *Service) GetLatest(ctx context.Context, siteID uuid.UUID) (Report, error) {
	return service.store.GetLatestCompatibilityCheck(ctx, siteID)
}

func (service *Service) ListLatest(ctx context.Context) ([]Report, error) {
	return service.store.ListLatestCompatibilityChecks(ctx)
}

func legacyFallback(err error) bool {
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) {
		return false
	}
	return failure.Kind == reconciliation.FailureAuthentication || failure.Code == "gateway_http_404" || failure.Code == "gateway_http_503"
}
