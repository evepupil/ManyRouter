package routing_test

import (
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

const legacySnapshotJSON = `{"schema_version":1,"site_id":"10000000-0000-0000-0000-000000000001","relation_id":"30000000-0000-0000-0000-000000000001","supplier_id":"20000000-0000-0000-0000-000000000001","group":{"key":"mr_s_30000000000000000000000000000001","display_name":"Supplier","sale_ratio":"1","visible":true},"channel":{"id":"40000000-0000-0000-0000-000000000001","managed_tag":"manyrouter:30000000-0000-0000-0000-000000000001","name":"Supplier [ManyRouter]","protocol":"openai_compatible","base_url":"https://upstream.example","credential_id":"50000000-0000-0000-0000-000000000001","credential_version":1,"models":[{"model":"a","upstream_model":"a"}],"group_key":"mr_s_30000000000000000000000000000001","priority":0,"weight":100,"desired_status":"enabled"}}`

func TestM0SnapshotRemainsByteCompatible(t *testing.T) {
	t.Parallel()
	snapshot, err := routing.DecodeSnapshot([]byte(legacySnapshotJSON))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := routing.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != legacySnapshotJSON {
		t.Fatalf("M0 snapshot was rewritten: %s", encoded)
	}
}

func siteSnapshotFixture(t *testing.T) routing.Snapshot {
	t.Helper()
	resource, err := routing.DecodeSnapshot([]byte(legacySnapshotJSON))
	if err != nil {
		t.Fatal(err)
	}
	return routing.Snapshot{
		SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: resource.SiteID,
		RelationID: resource.RelationID, SupplierID: resource.SupplierID,
		Resources: []routing.Snapshot{resource}, BillingBasisHash: strings.Repeat("1", 64),
	}
}

func TestSiteSnapshotRoundTripAndGroupReferences(t *testing.T) {
	t.Parallel()
	snapshot := siteSnapshotFixture(t)
	snapshot.AutoGroups = []routing.DesiredGroup{{Key: "mrab", DisplayName: "Balanced", SaleRatio: "1.25", Visible: true}}
	snapshot.Resources[0].Channel.ExtraGroupKeys = []string{"mrab"}
	snapshot.ResumeRelationIDs = []uuid.UUID{snapshot.RelationID}
	snapshot.PriceVersionIDs = []uuid.UUID{uuid.New()}
	snapshot.StrategyVersions = []routing.StrategyReference{{ID: uuid.New(), Version: 3}}
	encoded, expectedHash, err := routing.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := routing.DecodeSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	_, actualHash, err := routing.EncodeSnapshot(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if actualHash != expectedHash || len(decoded.Resources[0].Channel.GroupKeys()) != 2 {
		t.Fatal("site snapshot or channel groups changed during round trip")
	}
}

func TestSiteSnapshotAcceptsLegacyAutoGroupKey(t *testing.T) {
	t.Parallel()
	snapshot := siteSnapshotFixture(t)
	snapshot.AutoGroups = []routing.DesiredGroup{{Key: "mr_a_test", DisplayName: "Balanced", SaleRatio: "1.25", Visible: true}}
	snapshot.Resources[0].Channel.ExtraGroupKeys = []string{"mr_a_test"}
	if err := routing.ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("legacy Auto group key was rejected: %v", err)
	}
}

func TestSiteSnapshotRejectsInvalidOwnershipAndNestedPlans(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*routing.Snapshot){
		"other site":            func(s *routing.Snapshot) { s.Resources[0].SiteID = uuid.New() },
		"nested site plan":      func(s *routing.Snapshot) { s.Resources[0].SchemaVersion = routing.SiteSnapshotSchemaVersion },
		"unknown Auto":          func(s *routing.Snapshot) { s.Resources[0].Channel.ExtraGroupKeys = []string{"mr_a_missing"} },
		"duplicate relation":    func(s *routing.Snapshot) { s.Resources = append(s.Resources, s.Resources[0]) },
		"foreign resume":        func(s *routing.Snapshot) { s.ResumeRelationIDs = []uuid.UUID{uuid.New()} },
		"missing billing basis": func(s *routing.Snapshot) { s.BillingBasisHash = "" },
		"invalid strategy version": func(s *routing.Snapshot) {
			s.StrategyVersions = []routing.StrategyReference{{ID: uuid.New(), Version: 0}}
		},
		"duplicate strategy": func(s *routing.Snapshot) {
			id := uuid.New()
			s.StrategyVersions = []routing.StrategyReference{{ID: id, Version: 1}, {ID: id, Version: 2}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := siteSnapshotFixture(t)
			mutate(&snapshot)
			if err := routing.ValidateSnapshot(snapshot); err == nil {
				t.Fatal("invalid site snapshot was accepted")
			}
		})
	}
}
