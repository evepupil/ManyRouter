package postgres

import (
	"strings"
	"testing"
)

func TestOperatorJSONRemovesCredentialReferencesAtEveryDepth(t *testing.T) {
	raw := []byte(`{"credential_id":"root","snapshot":{"resources":[{"channel":{"credential_id":"nested","credential_version":3}}]},"pending_credential_id":"pending","name":"Supplier"}`)
	clean, err := sanitizeOperationJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(clean)
	for _, secretField := range []string{"root", "nested", "pending", "credential_id"} {
		if strings.Contains(text, secretField) {
			t.Fatalf("credential reference remained in operator response: %s", text)
		}
	}
	if !strings.Contains(text, `"credential_version":3`) || !strings.Contains(text, `"name":"Supplier"`) {
		t.Fatalf("business fields were removed with credential references: %s", text)
	}
}
