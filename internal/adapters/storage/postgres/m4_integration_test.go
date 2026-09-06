//go:build integration

package postgres_test

import (
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
)

func TestM4CompatibilityHistoryAndRuntimeFactsStaySiteScoped(t *testing.T) {
	ctx, store := openM2IntegrationStore(t)
	first := createM2IntegrationFixture(t, ctx, store)
	second := createM2IntegrationFixture(t, ctx, store)
	now := time.Now().UTC().Truncate(time.Microsecond)
	report := compatibility.Report{
		ID: uuid.New(), SiteID: first.siteID, SiteCode: "first", SiteName: "First",
		Mode: compatibility.ModeManaged, Verdict: compatibility.VerdictCompatible,
		CatalogVersion: "catalog-1", NewAPIVersion: "build-1", ContractVersion: "m4-managed-sync-v1",
		DatabaseType: "postgres", StateHash: strings.Repeat("a", 64), BillingBasisHash: strings.Repeat("b", 64),
		Conflicts: []string{}, Reasons: []compatibility.Reason{}, CheckedBy: "integration", CheckedAt: now,
	}
	if err := store.SaveCompatibilityCheck(ctx, report, site.CompatibilityCompatible); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestCompatibilityCheck(ctx, first.siteID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != report.ID || latest.Verdict != compatibility.VerdictCompatible || latest.SiteID != first.siteID {
		t.Fatalf("latest compatibility report = %#v", latest)
	}
	checks, err := store.ListLatestCompatibilityChecks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seenFirst := false
	seenSecond := false
	for _, check := range checks {
		seenFirst = seenFirst || check.SiteID == first.siteID
		seenSecond = seenSecond || check.SiteID == second.siteID
	}
	if !seenFirst || seenSecond {
		t.Fatalf("site-scoped latest checks: first=%v second=%v", seenFirst, seenSecond)
	}
	var compatibilityStatus string
	if err := store.Pool().QueryRow(ctx, `SELECT compatibility_status FROM sites WHERE id=$1`, first.siteID).Scan(&compatibilityStatus); err != nil {
		t.Fatal(err)
	}
	if compatibilityStatus != "compatible" {
		t.Fatalf("site compatibility status = %q", compatibilityStatus)
	}
	capability := completeManagedCapability("build-1")
	approved, err := store.ManagedSyncApproved(ctx, first.siteID, capability)
	if err != nil || !approved {
		t.Fatalf("managed sync approval = %v error=%v", approved, err)
	}
	capability.NewAPIVersion = "another-build"
	approved, err = store.ManagedSyncApproved(ctx, first.siteID, capability)
	if err != nil || approved {
		t.Fatalf("mismatched managed sync approval = %v error=%v", approved, err)
	}
	systemFacts, err := store.ReadRuntimeSystemFacts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !systemFacts.DatabaseUp || systemFacts.MigrationVersion < 8 {
		t.Fatalf("system facts = %#v", systemFacts)
	}
	siteFacts, err := store.ListRuntimeSiteFacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, facts := range siteFacts {
		if facts.SiteID != first.siteID {
			continue
		}
		found = true
		if facts.Compatibility == nil || facts.Compatibility.ID != report.ID || facts.RelationCount != 1 {
			t.Fatalf("runtime site facts = %#v", facts)
		}
	}
	if !found {
		t.Fatal("runtime facts did not include the first site")
	}
}

func completeManagedCapability(version string) reconciliation.ManagedSyncCapabilities {
	return reconciliation.ManagedSyncCapabilities{
		NewAPIVersion: version, ContractVersion: reconciliation.ManagedSyncContractVersion, DatabaseType: "postgres",
	}
}
