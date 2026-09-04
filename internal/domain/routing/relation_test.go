package routing_test

import (
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestNewRelationRejectsUnsupportedRatioPrecisionAndRange(t *testing.T) {
	t.Parallel()
	for _, ratio := range []string{"0", "1.0000001", "1000000"} {
		_, err := routing.NewRelation(uuid.New(), uuid.New(), uuid.New(), "Supplier", decimal.RequireFromString(ratio), true, time.Now())
		if err == nil {
			t.Fatalf("sale ratio %s was accepted", ratio)
		}
	}
}
