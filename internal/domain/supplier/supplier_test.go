package supplier_test

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestNewRejectsDuplicateModelsAndOutOfRangePrices(t *testing.T) {
	t.Parallel()
	base := supplier.ModelInput{
		Name: "model-a", UpstreamName: "model-a", InputPrice: decimal.Zero,
		OutputPrice: decimal.Zero, Currency: "USD",
	}
	_, err := supplier.New(uuid.New(), "supplier-a", "Supplier A", "https://upstream.example", uuid.New(), []supplier.ModelInput{base, base}, time.Now())
	if err == nil {
		t.Fatal("duplicate models were accepted")
	}
	base.InputPrice = decimal.RequireFromString("10000000000")
	_, err = supplier.New(uuid.New(), "supplier-a", "Supplier A", "https://upstream.example", uuid.New(), []supplier.ModelInput{base}, time.Now())
	if err == nil {
		t.Fatal("out-of-range price was accepted")
	}
}
