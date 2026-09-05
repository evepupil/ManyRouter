package operations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
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

func TestAutoGroupsAreStableCompactAndSharedAcrossSites(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	want := map[string]string{
		"lowest_price": "mrap",
		"low_latency":  "mral",
		"high_sla":     "mras",
		"high_quality": "mraq",
		"balanced":     "mrab",
	}
	if len(operations.StrategyKinds) != len(want) {
		t.Fatalf("strategy kinds changed without assigning compact group keys: %v", operations.StrategyKinds)
	}
	autoGroups := make([]string, 0, len(operations.StrategyKinds))
	seen := make(map[string]bool, len(operations.StrategyKinds))
	for _, kind := range operations.StrategyKinds {
		group := operations.AutoGroupKey(first, kind)
		if group != want[kind] {
			t.Fatalf("unstable group key for %s: got %q want %q", kind, group, want[kind])
		}
		if len(group) != 4 {
			t.Fatalf("Auto group key must use four characters: %q", group)
		}
		if group != operations.AutoGroupKey(second, kind) {
			t.Fatalf("group key must be shared by independent site instances: %s", kind)
		}
		if seen[group] {
			t.Fatalf("duplicate Auto group key: %q", group)
		}
		seen[group] = true
		autoGroups = append(autoGroups, group)
	}

	supplierGroup := routing.GroupKey(uuid.New())
	if len(supplierGroup) != 37 {
		t.Fatalf("unexpected supplier group length: got %d", len(supplierGroup))
	}
	channelGroups := strings.Join(append([]string{supplierGroup}, autoGroups...), ",")
	if len(channelGroups) > 64 {
		t.Fatalf("supplier and Auto groups exceed New API channel limit: got %d", len(channelGroups))
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
