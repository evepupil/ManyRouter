package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

var ErrKeyReused = errors.New("idempotency key was already used with a different request")

type Record struct {
	Scope       string
	Key         string
	RequestHash string
	StatusCode  int
	Response    []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type Store interface {
	FindIdempotencyRecord(context.Context, string, string, time.Time) (*Record, error)
	SaveIdempotencyRecord(context.Context, Record) error
}

type Service struct {
	store Store
	now   func() time.Time
	ttl   time.Duration
}

func NewService(store Store, now func() time.Time, ttl time.Duration) (*Service, error) {
	if store == nil || now == nil || ttl <= 0 {
		return nil, errors.New("idempotency dependencies are required")
	}
	return &Service{store: store, now: now, ttl: ttl}, nil
}

func (s *Service) Lookup(ctx context.Context, scope, key string, request any) (*Record, string, error) {
	if scope == "" {
		return nil, "", errors.New("idempotency scope is required")
	}
	if !keyPattern.MatchString(key) {
		return nil, "", errors.New("idempotency key must contain 8-128 letters, digits, dots, underscores, colons, or hyphens")
	}
	hash, err := RequestHash(request)
	if err != nil {
		return nil, "", err
	}
	record, err := s.store.FindIdempotencyRecord(ctx, scope, key, s.now().UTC())
	if err != nil {
		return nil, "", err
	}
	if record != nil && record.RequestHash != hash {
		return nil, "", ErrKeyReused
	}
	return record, hash, nil
}

func (s *Service) Save(ctx context.Context, scope, key, requestHash string, statusCode int, response any) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	now := s.now().UTC()
	return s.store.SaveIdempotencyRecord(ctx, Record{
		Scope:       scope,
		Key:         key,
		RequestHash: requestHash,
		StatusCode:  statusCode,
		Response:    payload,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.ttl),
	})
}

func RequestHash(request any) (string, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode idempotent request: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
