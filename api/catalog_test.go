package api_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestCatalogContractIsValid(t *testing.T) {
	t.Parallel()
	document, err := openapi3.NewLoader().LoadFromFile("catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
}
