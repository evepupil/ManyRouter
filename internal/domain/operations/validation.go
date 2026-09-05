package operations

import (
	"fmt"
	"strings"

	"github.com/evepupil/ManyRouter/internal/domain/value"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var StrategyKinds = []string{"lowest_price", "low_latency", "high_sla", "high_quality", "balanced"}

func ValidStrategyKind(kind string) bool {
	for _, allowed := range StrategyKinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

func AutoGroupKey(_ uuid.UUID, kind string) string {
	switch kind {
	case "lowest_price":
		return "mrap"
	case "low_latency":
		return "mral"
	case "high_sla":
		return "mras"
	case "high_quality":
		return "mraq"
	case "balanced":
		return "mrab"
	default:
		return ""
	}
}

func ValidateReason(reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return fmt.Errorf("%w: 请填写操作原因，长度不超过 1000 字节", ErrInvalid)
	}
	return nil
}

func ValidateRatio(raw string) (decimal.Decimal, error) {
	ratio, err := decimal.NewFromString(raw)
	if err != nil || !ratio.IsPositive() || ratio.Exponent() < -6 || ratio.GreaterThan(decimal.RequireFromString("999999.999999")) {
		return decimal.Zero, fmt.Errorf("%w: 销售倍率必须大于零，最多六位小数", ErrInvalid)
	}
	return ratio, nil
}

func ValidateStrategy(kind string, input StrategyInput) error {
	if !ValidStrategyKind(kind) || input.Version < 0 {
		return fmt.Errorf("%w: 策略或版本无效", ErrInvalid)
	}
	if _, err := value.NormalizeName(input.DisplayName, 120); err != nil {
		return fmt.Errorf("%w: 策略名称无效", ErrInvalid)
	}
	if err := ValidateReason(input.Reason); err != nil {
		return err
	}
	if input.Enabled && len(input.MemberRelationIDs) == 0 {
		return fmt.Errorf("%w: 启用策略前至少选择一家供应商", ErrInvalid)
	}
	seen := make(map[uuid.UUID]bool)
	for _, id := range input.MemberRelationIDs {
		if id == uuid.Nil || seen[id] {
			return fmt.Errorf("%w: 策略成员为空或重复", ErrInvalid)
		}
		seen[id] = true
	}
	return nil
}

func NormalizeFilter(input Filter) (Filter, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || len(input.Query) > 200 {
		return Filter{}, fmt.Errorf("%w: 分页或查询条件无效", ErrInvalid)
	}
	return input, nil
}
