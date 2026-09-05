package api

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOperationsContractIsValid(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("operations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
