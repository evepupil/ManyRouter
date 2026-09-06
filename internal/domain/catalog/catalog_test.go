package catalog_test

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

func TestBuildUsesConfirmedMembersAndAutoTraffic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	siteID, planID := uuid.New(), uuid.New()
	firstRelation, secondRelation := uuid.New(), uuid.New()
	quality := 88.0
	input := catalog.BuildInput{
		SiteID: siteID, SiteName: "站点", RoutePlanID: planID, Now: now,
		Plan: routing.Snapshot{
			SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: siteID,
			AutoGroups: []routing.DesiredGroup{{Key: "mrab", DisplayName: "均衡", SaleRatio: "1.2", Visible: true}},
			Resources: []routing.Snapshot{
				{RelationID: firstRelation, Channel: routing.DesiredChannel{DesiredStatus: routing.DesiredEnabled, Models: []routing.ModelRoute{{Model: "model-a"}}, ExtraGroupKeys: []string{"mrab"}}, Group: routing.DesiredGroup{Key: "one", DisplayName: "线路一", SaleRatio: "1", Visible: true}},
				{RelationID: secondRelation, Channel: routing.DesiredChannel{DesiredStatus: routing.DesiredEnabled, Models: []routing.ModelRoute{{Model: "model-a"}}, ExtraGroupKeys: []string{"mrab"}}, Group: routing.DesiredGroup{Key: "two", DisplayName: "线路二", SaleRatio: "1", Visible: true}},
			},
		},
		Metrics: map[catalog.MetricKey]catalog.MetricEvidence{
			{Group: "mrab", Model: "model-a"}: {RequestCount: 100, SuccessCount: 99, FactsThrough: now.Add(-time.Minute)},
		},
		Qualities: map[catalog.QualityKey]catalog.QualityEvidence{
			{RelationID: firstRelation, Model: "model-a"}:  {Score: &quality, Confidence: "high", Authenticity: "consistent"},
			{RelationID: secondRelation, Model: "model-a"}: {Score: &quality, Confidence: "medium", Authenticity: "consistent"},
		},
	}
	snapshot, err := catalog.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	var auto *catalog.Product
	for index := range snapshot.Products {
		if snapshot.Products[index].Kind == catalog.ProductFixedAuto {
			auto = &snapshot.Products[index]
		}
	}
	if auto == nil || auto.AvailableSuppliers != 2 || !auto.FailoverReady || auto.Status != "available" || auto.SLAPercent == nil || *auto.SLAPercent != 99 {
		t.Fatalf("unexpected Auto product: %#v", auto)
	}
}

func TestBuildDoesNotPresentMemberEvidenceAsAutoTraffic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	siteID, relationID := uuid.New(), uuid.New()
	quality := 95.0
	snapshot, err := catalog.Build(catalog.BuildInput{
		SiteID: siteID, SiteName: "站点", RoutePlanID: uuid.New(), Now: now,
		Plan: routing.Snapshot{
			SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: siteID,
			AutoGroups: []routing.DesiredGroup{{Key: "mrab", DisplayName: "均衡", SaleRatio: "1", Visible: true}},
			Resources: []routing.Snapshot{{
				RelationID: relationID,
				Channel:    routing.DesiredChannel{DesiredStatus: routing.DesiredEnabled, Models: []routing.ModelRoute{{Model: "model-a"}}, ExtraGroupKeys: []string{"mrab"}},
				Group:      routing.DesiredGroup{Key: "dedicated", DisplayName: "专属", SaleRatio: "1", Visible: true},
			}},
		},
		Qualities: map[catalog.QualityKey]catalog.QualityEvidence{
			{RelationID: relationID, Model: "model-a"}: {Score: &quality, Confidence: "high", Authenticity: "consistent"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range snapshot.Products {
		if product.Kind == catalog.ProductFixedAuto && (product.Status != "insufficient" || product.SLAPercent != nil) {
			t.Fatalf("Auto without group traffic must stay insufficient: %#v", product)
		}
	}
}
