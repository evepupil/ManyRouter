package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/evepupil/ManyRouter/internal/domain/credential"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (s *Service) prepare(ctx context.Context, m *domain.Mutation) error {
	m.Bases = make(map[uuid.UUID]domain.BillingBasis)
	switch input := m.Input.(type) {
	case domain.SiteInput:
		name, err := value.NormalizeName(input.Name, 120)
		if err != nil {
			return invalid("站点名称无效")
		}
		input.Name = name
		base, err := value.NormalizeHTTPBaseURL(input.NewAPIBaseURL)
		if err != nil {
			return invalid("站点地址无效")
		}
		input.NewAPIBaseURL = base
		if input.AdminUserID == 0 {
			input.AdminUserID = 1
		}
		if input.AdminUserID < 1 {
			return invalid("站点管理账号编号无效")
		}
		if m.Kind == "create_site" {
			code, err := value.NormalizeCode(input.Code)
			if err != nil {
				return invalid("站点代码无效")
			}
			input.Code = code
			input.Status = "enabled"
		} else {
			if input.Version < 1 || (input.Status != "enabled" && input.Status != "disabled") {
				return invalid("站点状态或版本无效")
			}
			if err := domain.ValidateReason(input.Reason); err != nil {
				return err
			}
		}
		if m.Kind == "update_site" {
			if err := s.checkSiteAccess(ctx, m.ID, input); err != nil {
				return err
			}
		}
		if m.Kind == "create_site" || input.AccessToken != "" {
			if err := s.seal(m, credential.PurposeNewAPIAdmin, input.AccessToken); err != nil {
				return err
			}
		}
		input.AccessToken = ""
		m.Input = input
	case domain.SupplierInput:
		for index := range input.Models {
			input.Models[index].Model = strings.TrimSpace(input.Models[index].Model)
			input.Models[index].UpstreamModel = strings.TrimSpace(input.Models[index].UpstreamModel)
			input.Models[index].Currency = strings.ToUpper(strings.TrimSpace(input.Models[index].Currency))
		}
		if m.Kind == "create_supplier" {
			input.Status = "enabled"
		} else {
			if input.Version < 1 || (input.Status != "enabled" && input.Status != "disabled") {
				return invalid("供应商状态或版本无效")
			}
			if err := domain.ValidateReason(input.Reason); err != nil {
				return err
			}
		}
		if err := validateModels(input.Models); err != nil {
			return err
		}
		name, err := value.NormalizeName(input.Name, 120)
		if err != nil {
			return invalid("供应商名称无效")
		}
		input.Name = name
		base, err := value.NormalizeOpenAICompatibleBaseURL(input.BaseURL)
		if err != nil {
			return invalid("供应商地址无效")
		}
		input.BaseURL = base
		if m.Kind == "create_supplier" {
			code, err := value.NormalizeCode(input.Code)
			if err != nil {
				return invalid("供应商代码无效")
			}
			input.Code = code
			if err := s.seal(m, credential.PurposeSupplierAPIKey, input.APIKey); err != nil {
				return err
			}
		}
		input.APIKey = ""
		m.Input = input
	case domain.CredentialInput:
		if input.Version < 1 {
			return invalid("供应商版本无效")
		}
		if err := domain.ValidateReason(input.Reason); err != nil {
			return err
		}
		access, err := s.store.GetSupplierAccess(ctx, m.ID)
		if err != nil {
			return err
		}
		if access.Version != input.Version {
			return domain.ErrConflict
		}
		if len(input.APIKey) < 8 || len(input.APIKey) > 16384 {
			return invalid("访问凭证长度无效")
		}
		if err := s.supplierCredentials.Check(ctx, access.BaseURL, []byte(input.APIKey)); err != nil {
			return invalid("新密钥未通过上游模型列表检查")
		}
		if err := s.seal(m, credential.PurposeSupplierAPIKey, input.APIKey); err != nil {
			return err
		}
		input.APIKey = ""
		m.Input = input
	case domain.CredentialCancelInput:
		if input.Version < 1 {
			return invalid("供应商版本无效")
		}
		if err := domain.ValidateReason(input.Reason); err != nil {
			return err
		}
		access, err := s.store.GetSupplierAccess(ctx, m.ID)
		if err != nil {
			return err
		}
		if access.Version != input.Version {
			return domain.ErrConflict
		}
		secret, err := s.vault.Decrypt(access.Credential)
		if err != nil {
			return err
		}
		defer clear(secret)
		if err := s.supplierCredentials.Check(ctx, access.BaseURL, secret); err != nil {
			return invalid("当前密钥未通过上游模型列表检查，不能取消候选版本")
		}
		return nil
	case domain.DeploymentInput:
		if input.SupplierID == uuid.Nil || len(input.Sites) == 0 || len(input.Sites) > 100 {
			return invalid("请选择供应商和目标站点")
		}
		if err := domain.ValidateReason(input.Reason); err != nil {
			return err
		}
		seen := make(map[uuid.UUID]bool)
		for _, target := range input.Sites {
			if target.SiteID == uuid.Nil || seen[target.SiteID] {
				return invalid("投放站点为空或重复")
			}
			seen[target.SiteID] = true
			if _, err := value.NormalizeName(target.DisplayName, 120); err != nil {
				return invalid("专属分组名称无效")
			}
			if _, err := domain.ValidateRatio(target.SaleRatio); err != nil {
				return err
			}
			basis, err := s.readBasis(ctx, target.SiteID)
			if err == nil {
				m.Bases[target.SiteID] = basis
			}
		}
	case domain.RelationInput:
		if input.Version < 1 || (input.DesiredStatus != "enabled" && input.DesiredStatus != "disabled") {
			return invalid("投放状态或版本无效")
		}
		if _, err := value.NormalizeName(input.DisplayName, 120); err != nil {
			return invalid("分组名称无效")
		}
		return domain.ValidateReason(input.Reason)
	case domain.StrategyInput:
		input.DisplayName = strings.TrimSpace(input.DisplayName)
		m.Input = input
		if err := domain.ValidateStrategy(m.StrategyKind, input); err != nil {
			return err
		}
		if input.Version == 0 {
			return s.checkNewAutoGroupOwnership(ctx, m.ID, m.StrategyKind)
		}
		return nil
	case domain.PriceInput:
		if input.SiteID == uuid.Nil || input.GroupKey == "" {
			return invalid("价格所属站点和分组不能为空")
		}
		if _, err := domain.ValidateRatio(input.SaleRatio); err != nil {
			return err
		}
		if err := domain.ValidateReason(input.Reason); err != nil {
			return err
		}
		basis, err := s.readBasis(ctx, input.SiteID)
		if err != nil {
			return err
		}
		m.Bases[input.SiteID] = basis
	case domain.PublishInput:
		if input.Version < 1 {
			return invalid("价格版本无效")
		}
		raw, err := s.store.GetOperationResource(ctx, "prices", m.ID)
		if err != nil {
			return err
		}
		var price struct {
			SiteID uuid.UUID `json:"site_id"`
		}
		if err := json.Unmarshal(raw, &price); err != nil {
			return err
		}
		basis, err := s.readBasis(ctx, price.SiteID)
		if err != nil {
			return err
		}
		m.Bases[price.SiteID] = basis
	case domain.RestoreInput:
		return domain.ValidateReason(input.Reason)
	case nil:
		if m.Kind != "sync" {
			return invalid("请求内容不能为空")
		}
		basis, err := s.readBasis(ctx, m.ID)
		if err == nil {
			m.Bases[m.ID] = basis
		}
	default:
		return invalid("操作类型无效")
	}
	return nil
}

func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }

func (s *Service) checkNewAutoGroupOwnership(ctx context.Context, siteID uuid.UUID, kind string) error {
	groupKey := domain.AutoGroupKey(siteID, kind)
	if groupKey == "" {
		return invalid("固定 Auto 分组类型无效")
	}
	access, err := s.store.GetSiteAccess(ctx, siteID)
	if err != nil {
		return err
	}
	secret, err := s.vault.Decrypt(access.Credential)
	if err != nil {
		return err
	}
	defer clear(secret)
	gateway, err := s.gateways.NewForSite(access.BaseURL, secret, access.AdminUserID)
	if err != nil {
		return err
	}
	actual, err := gateway.ReadActualState(ctx)
	if err != nil {
		return err
	}
	if _, exists := actual.GroupRatios[groupKey]; exists {
		return invalid("固定 Auto 分组短码已被站点现有配置占用，请先处理冲突")
	}
	if _, exists := actual.UserUsableGroups[groupKey]; exists {
		return invalid("固定 Auto 分组短码已被站点现有配置占用，请先处理冲突")
	}
	for _, channel := range actual.Channels {
		for _, group := range channel.Groups {
			if group == groupKey {
				return invalid("固定 Auto 分组短码已被站点现有配置占用，请先处理冲突")
			}
		}
	}
	return nil
}

func validateModels(models []domain.ModelInput) error {
	if len(models) == 0 || len(models) > 500 {
		return invalid("至少填写一个模型，最多 500 个")
	}
	enabled := 0
	seen := make(map[string]bool)
	max := decimal.RequireFromString("9999999999.9999999999")
	for _, model := range models {
		for _, name := range []string{model.Model, model.UpstreamModel} {
			if strings.TrimSpace(name) == "" || len(name) > 191 || strings.ContainsAny(name, ",\r\n") {
				return invalid("模型名称无效")
			}
		}
		if seen[model.Model] {
			return invalid("模型名称重复")
		}
		seen[model.Model] = true
		for _, price := range []string{model.InputPrice, model.OutputPrice} {
			v, err := decimal.NewFromString(price)
			if err != nil || v.IsNegative() || v.Exponent() < -10 || v.GreaterThan(max) {
				return invalid("模型采购价格无效")
			}
		}
		if model.Currency != "USD" && model.Currency != "CNY" {
			return invalid("采购价格币种须为 USD 或 CNY")
		}
		if model.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return invalid("至少启用一个模型")
	}
	return nil
}

func (s *Service) checkSiteAccess(ctx context.Context, id uuid.UUID, input domain.SiteInput) error {
	previous, err := s.store.GetSiteAccess(ctx, id)
	if err != nil {
		return err
	}
	if input.AccessToken == "" && input.AdminUserID == previous.AdminUserID && input.NewAPIBaseURL == previous.BaseURL {
		return nil
	}
	if input.NewAPIBaseURL != previous.BaseURL && input.AccessToken == "" {
		return invalid("修改站点地址时必须同时提供该地址的新管理凭证")
	}
	token := []byte(input.AccessToken)
	if len(token) == 0 {
		token, err = s.vault.Decrypt(previous.Credential)
		if err != nil {
			return err
		}
	}
	defer clear(token)
	gateway, err := s.gateways.NewForSite(input.NewAPIBaseURL, token, input.AdminUserID)
	if err != nil {
		return invalid("站点管理凭证配置无效")
	}
	if _, err = gateway.Probe(ctx); err != nil {
		return invalid("无法验证新的站点管理凭证")
	}
	if _, err = gateway.ReadActualState(ctx); err != nil {
		return invalid("新凭证未通过站点管理权限检查")
	}
	return nil
}
