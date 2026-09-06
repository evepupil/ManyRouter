package compatibility

import (
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
)

func TestEvaluateManagedCompatibility(t *testing.T) {
	manifest := Manifest{Version: "catalog-1", Combinations: []Combination{{
		Mode: ModeManaged, NewAPIVersion: "build-1", ContractVersion: reconciliation.ManagedSyncContractVersion,
		DatabaseTypes: []string{"postgres"}, Support: "recommended",
	}}}
	capability := completeCapability()
	stateValue := reconciliation.ManagedSyncState{
		StateHash: strings.Repeat("a", 64), BillingBasisHash: strings.Repeat("b", 64),
	}
	verdict, reasons := manifest.EvaluateManaged(capability, stateValue)
	if verdict != VerdictCompatible || len(reasons) != 0 {
		t.Fatalf("compatible verdict = %s reasons=%v", verdict, reasons)
	}

	capability.NewAPIVersion = "unknown-build"
	verdict, reasons = manifest.EvaluateManaged(capability, stateValue)
	if verdict != VerdictUnverified || len(reasons) != 1 || reasons[0].Code != "release_unverified" {
		t.Fatalf("unknown verdict = %s reasons=%v", verdict, reasons)
	}

	capability.NewAPIVersion = "build-1"
	capability.Features.AtomicApply = false
	verdict, reasons = manifest.EvaluateManaged(capability, stateValue)
	if verdict != VerdictIncompatible || len(reasons) == 0 || reasons[0].Code != "capability_missing" {
		t.Fatalf("missing capability verdict = %s reasons=%v", verdict, reasons)
	}
}

func TestEvaluateManagedRejectsConflictsAndNonPostgres(t *testing.T) {
	manifest := Manifest{Version: "catalog-1", Combinations: []Combination{{
		Mode: ModeManaged, NewAPIVersion: "build-1", ContractVersion: reconciliation.ManagedSyncContractVersion,
		DatabaseTypes: []string{"postgres"}, Support: "recommended",
	}}}
	capability := completeCapability()
	capability.DatabaseType = "sqlite"
	stateValue := reconciliation.ManagedSyncState{
		StateHash: strings.Repeat("a", 64), BillingBasisHash: strings.Repeat("b", 64),
		Conflicts: []string{"duplicate managed tag"},
	}
	verdict, reasons := manifest.EvaluateManaged(capability, stateValue)
	if verdict != VerdictIncompatible {
		t.Fatalf("verdict = %s reasons=%v", verdict, reasons)
	}
	codes := make(map[string]bool)
	for _, reason := range reasons {
		codes[reason.Code] = true
	}
	if !codes["database_unsupported"] || !codes["managed_resource_conflict"] {
		t.Fatalf("reason codes = %v", codes)
	}
}

func completeCapability() reconciliation.ManagedSyncCapabilities {
	return reconciliation.ManagedSyncCapabilities{
		ContractVersion: reconciliation.ManagedSyncContractVersion,
		NewAPIVersion:   "build-1",
		DatabaseType:    "postgres",
		Features: reconciliation.ManagedSyncFeatures{
			AtomicApply: true, ManagedChannels: true, MultipleGroups: true, GroupRatios: true,
			EntryVisibility: true, PersistentIdempotency: true, FinalStateDigest: true,
			LogRead: true,
		},
		Limits: reconciliation.ManagedSyncLimits{
			MaxChannels: 100, MaxGroups: 20, MaxModels: 500, MaxGroupKeyBytes: 64, MaxRequestBytes: 2 << 20,
		},
		RetryPolicy: reconciliation.RetryPolicy{RetryTimes: 2, StatusCodes: []reconciliation.StatusCodeRange{{Start: 429, End: 429}}},
	}
}
