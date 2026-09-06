package newapi

import (
	"context"
	"errors"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/credential"
	operationsdomain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

type automationAccessStore interface {
	GetSiteAccess(context.Context, uuid.UUID) (operationsdomain.SiteAccess, error)
}

type automationCredentialVault interface {
	Decrypt(credential.Record) ([]byte, error)
}

type AutomationChecker struct {
	store    automationAccessStore
	vault    automationCredentialVault
	gateways reconciliation.SiteGatewayFactory
}

func NewAutomationChecker(store automationAccessStore, vault automationCredentialVault, gateways reconciliation.SiteGatewayFactory) (*AutomationChecker, error) {
	if store == nil || vault == nil || gateways == nil {
		return nil, errors.New("automation compatibility dependencies are required")
	}
	return &AutomationChecker{store: store, vault: vault, gateways: gateways}, nil
}

func (checker *AutomationChecker) CheckAutomationCompatibility(ctx context.Context, siteID uuid.UUID) (automationapp.Compatibility, error) {
	access, err := checker.store.GetSiteAccess(ctx, siteID)
	if err != nil {
		return automationapp.Compatibility{}, err
	}
	secret, err := checker.vault.Decrypt(access.Credential)
	if err != nil {
		return automationapp.Compatibility{}, err
	}
	defer clear(secret)
	gateway, err := checker.gateways.NewForSite(access.BaseURL, secret, access.AdminUserID)
	if err != nil {
		return automationapp.Compatibility{}, err
	}
	reader, ok := gateway.(reconciliation.RetryPolicyReader)
	if !ok {
		return automationapp.Compatibility{Reasons: []string{"当前网关适配器无法读取重试配置"}}, nil
	}
	policy, err := reader.ReadRetryPolicy(ctx)
	if err != nil {
		return automationapp.Compatibility{}, err
	}
	reasons := make([]string, 0, 2)
	if policy.RetryTimes < 1 {
		reasons = append(reasons, "重试次数小于 1")
	}
	if !policy.AllowsStatus(httpStatusInternalServerError) {
		reasons = append(reasons, "HTTP 500 未配置为可重试")
	}
	return automationapp.Compatibility{Ready: len(reasons) == 0, Reasons: reasons}, nil
}

const httpStatusInternalServerError = 500

var _ automationapp.CompatibilityChecker = (*AutomationChecker)(nil)
