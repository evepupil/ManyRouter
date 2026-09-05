package operations_test

import (
	"errors"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

func TestStrategyRequiresMembersAndRejectsDuplicates(t *testing.T) {
	id := uuid.New()
	input := operations.StrategyInput{Enabled: true, DisplayName: "均衡", Reason: "人工准入", MemberRelationIDs: []uuid.UUID{id}}
	if err := operations.ValidateStrategy("balanced", input); err != nil {
		t.Fatal(err)
	}
	input.MemberRelationIDs = append(input.MemberRelationIDs, id)
	if err := operations.ValidateStrategy("balanced", input); !errors.Is(err, operations.ErrInvalid) {
		t.Fatalf("duplicate members accepted: %v", err)
	}
	input.MemberRelationIDs = nil
	if err := operations.ValidateStrategy("balanced", input); !errors.Is(err, operations.ErrInvalid) {
		t.Fatalf("empty enabled strategy accepted: %v", err)
	}
	input.Enabled = false
	if err := operations.ValidateStrategy("balanced", input); err != nil {
		t.Fatal(err)
	}
}

func TestAutoGroupsAreStableAndSiteScoped(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	a := operations.AutoGroupKey(first, "balanced")
	if a != operations.AutoGroupKey(first, "balanced") {
		t.Fatal("group identity changed")
	}
	if a == operations.AutoGroupKey(second, "balanced") || a == operations.AutoGroupKey(first, "lowest_price") {
		t.Fatal("group identity leaked across a site or strategy")
	}
}

func TestPricePrecisionAndPaginationBounds(t *testing.T) {
	for _, raw := range []string{"0", "-1", "1.0000001", "1000000", "bad"} {
		if _, err := operations.ValidateRatio(raw); err == nil {
			t.Fatalf("invalid price accepted: %s", raw)
		}
	}
	ratio, err := operations.ValidateRatio("1.250000")
	if err != nil || ratio.String() != "1.25" {
		t.Fatalf("price normalization: %v %v", ratio, err)
	}
	if _, err := operations.NormalizeFilter(operations.Filter{Limit: 101}); err == nil {
		t.Fatal("unbounded page accepted")
	}
}
