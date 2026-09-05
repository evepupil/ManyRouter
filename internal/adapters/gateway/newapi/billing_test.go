package newapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
)

func TestBillingBasisNormalizesPricingAndExcludesSecretsAndManagedGroups(t *testing.T) {
	t.Parallel()
	modelRatio, groupRatio, version := `{"b":2.00,"a":1}`, `{"default":1}`, "version-a"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("New-Api-User") != "37" {
			t.Errorf("wrong management user header: %s", request.Header.Get("New-Api-User"))
		}
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"version": version}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]string{
			{"key": "ModelRatio", "value": modelRatio}, {"key": "CompletionRatio", "value": `{"a":2}`},
			{"key": "ModelPrice", "value": `{}`}, {"key": "GroupRatio", "value": groupRatio},
			{"key": "UserUsableGroups", "value": `{"default":"Default"}`}, {"key": "SMTPToken", "value": "private-example"},
		}})
	}))
	defer server.Close()
	gateway, err := (newapi.Factory{HTTPClient: server.Client()}).NewForSite(server.URL, []byte("admin-example"), 37)
	if err != nil {
		t.Fatal(err)
	}
	reader := gateway.(reconciliation.BillingBasisReader)
	basis, firstHash, err := reader.ReadBillingBasis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(basis) != 4 || basis["SMTPToken"] != nil || basis["GroupRatio"] != nil || string(basis["NewAPIVersion"]) != `"version-a"` {
		t.Fatalf("unexpected durable basis: %#v", basis)
	}
	modelRatio, groupRatio = `{"a":1.0,"b":2}`, `{"default":9}`
	_, normalizedHash, err := reader.ReadBillingBasis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if normalizedHash != firstHash {
		t.Fatal("JSON order, numeric representation or managed group updates changed the pricing baseline")
	}
	version = "version-b"
	_, upgradedHash, err := reader.ReadBillingBasis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if upgradedHash == firstHash {
		t.Fatal("New API upgrade did not invalidate pricing baseline")
	}
	modelRatio = `{"a":3,"b":2}`
	_, changedHash, err := reader.ReadBillingBasis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == upgradedHash {
		t.Fatal("model pricing change did not invalidate baseline")
	}
}

func TestBillingBasisRequiresPublishedModelPricingSettings(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"version":"test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"key":"ModelRatio","value":"{}"}]}`))
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-example"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ReadBillingBasis(context.Background()); err == nil {
		t.Fatal("incomplete pricing baseline was accepted")
	}
}
