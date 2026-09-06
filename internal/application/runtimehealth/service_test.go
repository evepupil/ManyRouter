package runtimehealth

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
	"github.com/google/uuid"
)

func TestEvaluateSiteLevels(t *testing.T) {
	now := time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * time.Minute)
	base := SiteFacts{
		SiteID: uuid.New(), SiteStatus: "enabled", RelationCount: 1,
		Compatibility: &compatibility.Report{Verdict: compatibility.VerdictCompatible, CheckedAt: recent},
		Route:         RouteFacts{ConfirmedAt: &recent, LatestPlanStatus: "confirmed", LastSyncStatus: "succeeded"},
		Collection:    CollectionFacts{LastSuccessAt: &recent},
		Scoring:       ScoringFacts{CompletedAt: &recent},
		Product:       ProductFacts{GeneratedAt: &recent},
	}
	level, reasons := evaluateSite(base, now)
	if level != LevelNormal || len(reasons) != 0 {
		t.Fatalf("normal site = %s reasons=%v", level, reasons)
	}

	stale := base
	old := now.Add(-2 * time.Hour)
	stale.Collection.LastSuccessAt = &old
	level, reasons = evaluateSite(stale, now)
	if level != LevelAttention || !hasReason(reasons, "collection_stale") {
		t.Fatalf("stale site = %s reasons=%v", level, reasons)
	}

	blocked := base
	blocked.Compatibility = &compatibility.Report{Verdict: compatibility.VerdictUnverified, CheckedAt: recent}
	level, reasons = evaluateSite(blocked, now)
	if level != LevelBlocked || !hasReason(reasons, "compatibility_blocked") {
		t.Fatalf("blocked site = %s reasons=%v", level, reasons)
	}

	fault := base
	fault.Collection.ConsecutiveFailures = 3
	level, reasons = evaluateSite(fault, now)
	if level != LevelFault || !hasReason(reasons, "collection_failed") {
		t.Fatalf("fault site = %s reasons=%v", level, reasons)
	}
}

func TestEvaluateSystemLevels(t *testing.T) {
	now := time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC)
	old := now.Add(-31 * time.Minute)
	level, reasons := evaluateSystem(SystemFacts{
		DatabaseUp: true, Jobs: JobFacts{OldestWaitingAt: &old},
	}, now)
	if level != LevelFault || !hasReason(reasons, "jobs_stalled") {
		t.Fatalf("system = %s reasons=%v", level, reasons)
	}
	level, reasons = evaluateSystem(SystemFacts{DatabaseUp: false}, now)
	if level != LevelFault || !hasReason(reasons, "database_unavailable") {
		t.Fatalf("database system = %s reasons=%v", level, reasons)
	}
}

func hasReason(reasons []Reason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
