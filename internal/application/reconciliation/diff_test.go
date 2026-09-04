package reconciliation_test

import (
	"errors"
	"testing"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
)

func TestLocateManagedChannelRejectsDuplicateTags(t *testing.T) {
	t.Parallel()
	expected := routing.DesiredChannel{ManagedTag: "manyrouter:relation"}
	_, err := reconciliation.LocateManagedChannel(expected, nil, []reconciliation.ActualChannel{
		{ID: 1, ManagedTag: expected.ManagedTag},
		{ID: 2, ManagedTag: expected.ManagedTag},
	})
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Kind != reconciliation.FailureOwnership {
		t.Fatalf("expected ownership failure, got %v", err)
	}
}

func TestLocateManagedChannelRejectsMismatchedBinding(t *testing.T) {
	t.Parallel()
	id := int64(7)
	_, err := reconciliation.LocateManagedChannel(
		routing.DesiredChannel{ManagedTag: "manyrouter:expected"},
		&id,
		[]reconciliation.ActualChannel{{ID: id, ManagedTag: "someone-else"}},
	)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Code != "binding_mismatch" {
		t.Fatalf("expected binding mismatch, got %v", err)
	}
}

func TestMergeGroupRatiosPreservesUnmanagedGroups(t *testing.T) {
	t.Parallel()
	merged, changed, err := reconciliation.MergeGroupRatios(
		map[string]string{"default": "1.000", "operator": "2.5"},
		"mr_s_relation",
		"1.250000",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || merged["default"] != "1" || merged["operator"] != "2.5" || merged["mr_s_relation"] != "1.25" {
		t.Fatalf("unexpected merge result: %#v", merged)
	}
	_, changed, err = reconciliation.MergeGroupRatios(merged, "mr_s_relation", "1.25")
	if err != nil || changed {
		t.Fatalf("expected idempotent merge, changed=%v err=%v", changed, err)
	}
}

func TestVerifyUnmanagedGroupRatiosDetectsLostConfiguration(t *testing.T) {
	t.Parallel()
	err := reconciliation.VerifyUnmanagedGroupRatios(
		map[string]string{"default": "1", "operator": "2"},
		map[string]string{"default": "1", "managed": "1.25"},
		"managed",
	)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Code != "unmanaged_group_removed" {
		t.Fatalf("expected unmanaged group failure, got %v", err)
	}
}

func TestMergeUserUsableGroupsPreservesExistingDescriptions(t *testing.T) {
	t.Parallel()
	group := routing.DesiredGroup{Key: "mr_s_relation", DisplayName: "Supplier A", Visible: true}
	merged, changed := reconciliation.MergeUserUsableGroups(map[string]string{"default": "Default"}, group)
	if !changed || merged["default"] != "Default" || merged[group.Key] != group.DisplayName {
		t.Fatalf("unexpected visible group merge: %#v", merged)
	}
	group.Visible = false
	merged, changed = reconciliation.MergeUserUsableGroups(merged, group)
	if !changed {
		t.Fatal("hiding the managed group did not produce a change")
	}
	if _, exists := merged[group.Key]; exists || merged["default"] != "Default" {
		t.Fatalf("hidden group merge changed unmanaged groups: %#v", merged)
	}
}

func TestVerifyUnmanagedUserGroupsDetectsChangedDescription(t *testing.T) {
	t.Parallel()
	err := reconciliation.VerifyUnmanagedUserGroups(
		map[string]string{"default": "Default"},
		map[string]string{"default": "Changed", "managed": "Supplier"},
		"managed",
	)
	var failure *reconciliation.Failure
	if !errors.As(err, &failure) || failure.Code != "unmanaged_user_group_changed" {
		t.Fatalf("expected unmanaged user group failure, got %v", err)
	}
}
