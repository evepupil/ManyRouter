package idempotency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/idempotency"
)

type memoryStore struct {
	record *idempotency.Record
}

func (s *memoryStore) FindIdempotencyRecord(context.Context, string, string, time.Time) (*idempotency.Record, error) {
	return s.record, nil
}

func (s *memoryStore) SaveIdempotencyRecord(_ context.Context, record idempotency.Record) error {
	s.record = &record
	return nil
}

func TestLookupRejectsSameKeyForDifferentRequest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{}
	service, err := idempotency.NewService(store, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := service.Lookup(context.Background(), "create-site", "request-123", map[string]string{"code": "one"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Save(context.Background(), "create-site", "request-123", hash, 201, map[string]string{"id": "one"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Lookup(context.Background(), "create-site", "request-123", map[string]string{"code": "two"}); !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatalf("expected reused key error, got %v", err)
	}
}
