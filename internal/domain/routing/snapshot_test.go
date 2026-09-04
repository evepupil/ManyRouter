package routing_test

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestBuildSnapshotIsStableAcrossModelInputOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	siteID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	supplierID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	relationID := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	channelID := uuid.MustParse("40000000-0000-0000-0000-000000000001")
	supplierCredentialID := uuid.MustParse("50000000-0000-0000-0000-000000000001")

	siteData, err := site.New(siteID, "site-a", "Site A", "https://gateway.example.com/", uuid.New(), now)
	if err != nil {
		t.Fatal(err)
	}
	newSupplier := func(models []supplier.ModelInput) supplier.Supplier {
		data, createErr := supplier.New(supplierID, "supplier-a", "Supplier A", "https://upstream.example.com/v1/", supplierCredentialID, models, now)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return data
	}
	modelA := supplier.ModelInput{Name: "model-a", UpstreamName: "upstream-a", InputPrice: decimal.RequireFromString("0.1"), OutputPrice: decimal.RequireFromString("0.2"), Currency: "USD"}
	modelB := supplier.ModelInput{Name: "model-b", UpstreamName: "model-b", InputPrice: decimal.Zero, OutputPrice: decimal.Zero, Currency: "USD"}
	relation, err := routing.NewRelation(relationID, siteID, supplierID, "Supplier A", decimal.RequireFromString("1.250000"), true, now)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := routing.NewManagedChannel(channelID, relationID, now)
	if err != nil {
		t.Fatal(err)
	}

	first, err := routing.BuildSnapshot(siteData, newSupplier([]supplier.ModelInput{modelB, modelA}), relation, channel)
	if err != nil {
		t.Fatal(err)
	}
	second, err := routing.BuildSnapshot(siteData, newSupplier([]supplier.ModelInput{modelA, modelB}), relation, channel)
	if err != nil {
		t.Fatal(err)
	}
	firstPayload, firstHash, err := routing.EncodeSnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, secondHash, err := routing.EncodeSnapshot(second)
	if err != nil {
		t.Fatal(err)
	}

	if string(firstPayload) != string(secondPayload) || firstHash != secondHash {
		t.Fatalf("snapshot changed with model order:\n%s\n%s", firstPayload, secondPayload)
	}
	if first.Group.SaleRatio != "1.25" {
		t.Fatalf("sale ratio was not normalized: %s", first.Group.SaleRatio)
	}
	if first.Channel.Models[0].Model != "model-a" {
		t.Fatalf("models were not sorted: %#v", first.Channel.Models)
	}
}

func TestStableManagedNamesDoNotUseDisplayNames(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	if got, want := routing.GroupKey(id), "mr_s_30000000000000000000000000000001"; got != want {
		t.Fatalf("group key = %q, want %q", got, want)
	}
	if got, want := routing.ManagedTag(id), "manyrouter:30000000-0000-0000-0000-000000000001"; got != want {
		t.Fatalf("managed tag = %q, want %q", got, want)
	}
}

func TestDecodeSnapshotRejectsMissingModels(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"schema_version":1,"site_id":"10000000-0000-0000-0000-000000000001","relation_id":"30000000-0000-0000-0000-000000000001","supplier_id":"20000000-0000-0000-0000-000000000001","group":{"key":"mr_s_30000000000000000000000000000001","display_name":"Supplier","sale_ratio":"1","visible":true},"channel":{"id":"40000000-0000-0000-0000-000000000001","managed_tag":"manyrouter:30000000-0000-0000-0000-000000000001","name":"Supplier [ManyRouter]","protocol":"openai_compatible","base_url":"https://upstream.example","credential_id":"50000000-0000-0000-0000-000000000001","credential_version":1,"models":[],"group_key":"mr_s_30000000000000000000000000000001","priority":0,"weight":100,"desired_status":"enabled"}}`)
	if _, err := routing.DecodeSnapshot(payload); err == nil {
		t.Fatal("snapshot without models was accepted")
	}
}
