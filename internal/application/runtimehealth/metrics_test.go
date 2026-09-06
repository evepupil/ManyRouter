package runtimehealth

import (
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
)

func TestPrometheusUsesSiteCodeAndNoDetailedIdentifiers(t *testing.T) {
	now := time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC)
	last := now.Add(-time.Minute)
	snapshot := Snapshot{
		GeneratedAt: now,
		System:      SystemSnapshot{BuildVersion: "m4", BuildCommit: "abc", Facts: SystemFacts{DatabaseUp: true}},
		Sites: []SiteSnapshot{{SiteFacts: SiteFacts{
			SiteCode: "site-a", SiteName: "Private customer", Compatibility: &compatibility.Report{Verdict: compatibility.VerdictCompatible},
			Route: RouteFacts{ConfirmedAt: &last}, Collection: CollectionFacts{LastSuccessAt: &last},
			Product: ProductFacts{GeneratedAt: &last},
		}}},
	}
	output := Prometheus(snapshot)
	if !strings.Contains(output, `manyrouter_site_compatible{site="site-a"} 1`) {
		t.Fatalf("missing site metric:\n%s", output)
	}
	if strings.Contains(output, "Private customer") {
		t.Fatalf("site name leaked into metrics:\n%s", output)
	}
}
