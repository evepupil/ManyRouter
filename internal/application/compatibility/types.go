package compatibility

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

type Mode string

const (
	ModeManaged Mode = "managed"
	ModeLegacy  Mode = "legacy"
	ModeUnknown Mode = "unknown"
)

type Verdict string

const (
	VerdictCompatible   Verdict = "compatible"
	VerdictUnverified   Verdict = "unverified"
	VerdictIncompatible Verdict = "incompatible"
	VerdictUnreachable  Verdict = "unreachable"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}

type Features struct {
	AtomicApply           bool `json:"atomic_apply"`
	ManagedChannels       bool `json:"managed_channels"`
	MultipleGroups        bool `json:"multiple_groups"`
	GroupRatios           bool `json:"group_ratios"`
	EntryVisibility       bool `json:"entry_visibility"`
	PersistentIdempotency bool `json:"persistent_idempotency"`
	FinalStateDigest      bool `json:"final_state_digest"`
	LogRead               bool `json:"log_read"`
}

type Limits struct {
	MaxChannels      int   `json:"max_channels"`
	MaxGroups        int   `json:"max_groups"`
	MaxModels        int   `json:"max_models"`
	MaxGroupKeyBytes int   `json:"max_group_key_bytes"`
	MaxRequestBytes  int64 `json:"max_request_bytes"`
}

type Capabilities struct {
	Features         Features `json:"features"`
	Limits           Limits   `json:"limits"`
	RetryTimes       int      `json:"retry_times"`
	RetryStatusCodes []string `json:"retry_status_codes"`
}

type Report struct {
	ID               uuid.UUID    `json:"id"`
	SiteID           uuid.UUID    `json:"site_id"`
	SiteCode         string       `json:"site_code"`
	SiteName         string       `json:"site_name"`
	Mode             Mode         `json:"mode"`
	Verdict          Verdict      `json:"verdict"`
	CatalogVersion   string       `json:"catalog_version"`
	NewAPIVersion    string       `json:"new_api_version"`
	ContractVersion  string       `json:"contract_version"`
	DatabaseType     string       `json:"database_type"`
	Capabilities     Capabilities `json:"capabilities"`
	StateHash        string       `json:"state_hash"`
	BillingBasisHash string       `json:"billing_basis_hash"`
	Conflicts        []string     `json:"conflicts"`
	Reasons          []Reason     `json:"reasons"`
	ErrorCode        string       `json:"error_code,omitempty"`
	ErrorMessage     string       `json:"error_message,omitempty"`
	CheckedBy        string       `json:"checked_by"`
	CheckedAt        time.Time    `json:"checked_at"`
}

type Combination struct {
	Mode                   Mode     `yaml:"mode"`
	NewAPIVersion          string   `yaml:"new_api_version"`
	ContractVersion        string   `yaml:"contract_version"`
	DatabaseTypes          []string `yaml:"database_types"`
	Support                string   `yaml:"support"`
	MinimumRollbackVersion string   `yaml:"minimum_rollback_version"`
	Notes                  []string `yaml:"notes"`
}

type Manifest struct {
	Version      string        `yaml:"version"`
	Combinations []Combination `yaml:"combinations"`
}

func (manifest Manifest) Validate() error {
	manifest.Version = strings.TrimSpace(manifest.Version)
	if manifest.Version == "" {
		return errors.New("compatibility catalog version is required")
	}
	if len(manifest.Combinations) == 0 {
		return errors.New("compatibility catalog must contain at least one combination")
	}
	seen := make(map[string]bool, len(manifest.Combinations))
	for _, combination := range manifest.Combinations {
		if combination.Mode != ModeManaged && combination.Mode != ModeLegacy {
			return fmt.Errorf("compatibility mode %q is invalid", combination.Mode)
		}
		if strings.TrimSpace(combination.NewAPIVersion) == "" {
			return errors.New("new API version is required in every compatibility combination")
		}
		if combination.Support != "recommended" && combination.Support != "supported" && combination.Support != "transitional" && combination.Support != "blocked" {
			return fmt.Errorf("compatibility support %q is invalid", combination.Support)
		}
		if combination.Mode == ModeManaged && strings.TrimSpace(combination.ContractVersion) == "" {
			return errors.New("managed compatibility combination requires a contract version")
		}
		key := string(combination.Mode) + "\x00" + combination.NewAPIVersion + "\x00" + combination.ContractVersion
		if seen[key] {
			return errors.New("compatibility catalog contains a duplicate combination")
		}
		seen[key] = true
	}
	return nil
}

func (manifest Manifest) EvaluateManaged(
	capability reconciliation.ManagedSyncCapabilities,
	stateValue reconciliation.ManagedSyncState,
) (Verdict, []Reason) {
	reasons := validateManagedCapability(capability, stateValue)
	if len(reasons) > 0 {
		return VerdictIncompatible, reasons
	}
	combination, found := manifest.find(ModeManaged, capability.NewAPIVersion, capability.ContractVersion, capability.DatabaseType)
	if !found {
		return VerdictUnverified, []Reason{{
			Code: "release_unverified", Message: "该 New API 构建尚未进入兼容清单。",
			Action: "先完成合同与真实冒烟验证，再开放自动发布。",
		}}
	}
	if combination.Support == "blocked" {
		return VerdictIncompatible, []Reason{{
			Code: "release_blocked", Message: "该版本组合已在兼容清单中停用。",
			Action: "切换到清单中的推荐版本。",
		}}
	}
	return VerdictCompatible, nil
}

func (manifest Manifest) EvaluateLegacy(version string) (Verdict, []Reason) {
	combination, found := manifest.find(ModeLegacy, version, "", "")
	if !found {
		return VerdictUnverified, []Reason{{
			Code: "legacy_release_unverified", Message: "该旧接口版本尚未进入兼容清单。",
			Action: "保留当前线路，并迁移到窄权限整批同步版本。",
		}}
	}
	if combination.Support == "blocked" {
		return VerdictIncompatible, []Reason{{
			Code: "legacy_release_blocked", Message: "该旧接口版本已停止支持。",
			Action: "升级 New API 后重新检查。",
		}}
	}
	return VerdictCompatible, []Reason{{
		Code: "legacy_transition", Message: "该站点仍通过旧管理接口同步。",
		Action: "配置站点同步令牌后迁移到整批同步。",
	}}
}

func (manifest Manifest) find(mode Mode, version, contract, database string) (Combination, bool) {
	for _, combination := range manifest.Combinations {
		if combination.Mode != mode || combination.NewAPIVersion != version || combination.ContractVersion != contract {
			continue
		}
		if database != "" && len(combination.DatabaseTypes) > 0 && !contains(combination.DatabaseTypes, database) {
			continue
		}
		return combination, true
	}
	return Combination{}, false
}

func validateManagedCapability(capability reconciliation.ManagedSyncCapabilities, stateValue reconciliation.ManagedSyncState) []Reason {
	reasons := make([]Reason, 0)
	if capability.ContractVersion != reconciliation.ManagedSyncContractVersion {
		reasons = append(reasons, Reason{Code: "contract_unsupported", Message: "站点同步合同版本不受支持。", Action: "升级到兼容清单中的 New API 版本。"})
	}
	features := capability.Features
	missing := make([]string, 0)
	for name, enabled := range map[string]bool{
		"atomic_apply": features.AtomicApply, "managed_channels": features.ManagedChannels,
		"multiple_groups": features.MultipleGroups, "group_ratios": features.GroupRatios,
		"entry_visibility": features.EntryVisibility, "persistent_idempotency": features.PersistentIdempotency,
		"final_state_digest": features.FinalStateDigest,
		"log_read":           features.LogRead,
	} {
		if !enabled {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		reasons = append(reasons, Reason{Code: "capability_missing", Message: "站点缺少正式同步所需能力：" + strings.Join(missing, "、") + "。", Action: "升级 New API 后重新检查。"})
	}
	if capability.DatabaseType != "postgres" {
		reasons = append(reasons, Reason{Code: "database_unsupported", Message: "正式组合只支持 PostgreSQL 站点。", Action: "迁移站点数据库后重新检查。"})
	}
	limits := capability.Limits
	if limits.MaxChannels < 1 || limits.MaxGroups < 1 || limits.MaxModels < 1 || limits.MaxGroupKeyBytes < 37 || limits.MaxRequestBytes < 1024 {
		reasons = append(reasons, Reason{Code: "limits_invalid", Message: "站点公布的整批同步容量不足。", Action: "检查 New API 构建和同步配置。"})
	}
	if !validDigest(stateValue.StateHash) || !validDigest(stateValue.BillingBasisHash) {
		reasons = append(reasons, Reason{Code: "state_digest_invalid", Message: "站点没有返回可核对的受管状态摘要。", Action: "检查 New API 同步接口后重新检查。"})
	}
	if len(stateValue.Conflicts) > 0 {
		reasons = append(reasons, Reason{Code: "managed_resource_conflict", Message: "站点存在受管资源归属冲突。", Action: "核对冲突渠道和分组后重新检查。"})
	}
	return reasons
}

func reportCapabilities(value reconciliation.ManagedSyncCapabilities) Capabilities {
	ranges := make([]string, 0, len(value.RetryPolicy.StatusCodes))
	for _, item := range value.RetryPolicy.StatusCodes {
		if item.Start == item.End {
			ranges = append(ranges, fmt.Sprintf("%d", item.Start))
		} else {
			ranges = append(ranges, fmt.Sprintf("%d-%d", item.Start, item.End))
		}
	}
	return Capabilities{
		Features: Features{
			AtomicApply: value.Features.AtomicApply, ManagedChannels: value.Features.ManagedChannels,
			MultipleGroups: value.Features.MultipleGroups, GroupRatios: value.Features.GroupRatios,
			EntryVisibility: value.Features.EntryVisibility, PersistentIdempotency: value.Features.PersistentIdempotency,
			FinalStateDigest: value.Features.FinalStateDigest,
			LogRead:          value.Features.LogRead,
		},
		Limits: Limits{
			MaxChannels: value.Limits.MaxChannels, MaxGroups: value.Limits.MaxGroups,
			MaxModels: value.Limits.MaxModels, MaxGroupKeyBytes: value.Limits.MaxGroupKeyBytes,
			MaxRequestBytes: value.Limits.MaxRequestBytes,
		},
		RetryTimes: value.RetryPolicy.RetryTimes, RetryStatusCodes: ranges,
	}
}

func siteCompatibilityStatus(verdict Verdict) site.CompatibilityStatus {
	if verdict == VerdictCompatible {
		return site.CompatibilityCompatible
	}
	return site.CompatibilityIncompatible
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
