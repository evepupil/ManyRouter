package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type attempt struct {
	count int32
	start time.Time
}

type memoryStore struct {
	mu       sync.Mutex
	operator *Operator
	sessions map[string]SessionRecord
	attempts map[string]attempt
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sessions: map[string]SessionRecord{}, attempts: map[string]attempt{}}
}

func (m *memoryStore) AuthInitialized(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operator != nil, nil
}

func (m *memoryStore) CreateInitialOperator(_ context.Context, operator Operator) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.operator != nil {
		return false, nil
	}
	m.operator = &operator
	return true, nil
}

func (m *memoryStore) FindOperator(_ context.Context, username string) (*Operator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.operator == nil || m.operator.User.Username != username {
		return nil, nil
	}
	operator := *m.operator
	return &operator, nil
}

func (m *memoryStore) SaveOperatorSession(_ context.Context, record SessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[record.TokenHash] = record
	return nil
}

func (m *memoryStore) FindOperatorSession(_ context.Context, hash string) (*SessionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[hash]
	if !ok {
		return nil, nil
	}
	record.Enabled = m.operator.Enabled
	record.User = m.operator.User
	return &record, nil
}

func (m *memoryStore) DeleteOperatorSession(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, hash)
	return nil
}

func (m *memoryStore) ConsumeAuthAttempt(_ context.Context, key string, now, cutoff time.Time) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.attempts[key]
	if !value.start.After(cutoff) {
		value = attempt{start: now}
	}
	value.count++
	m.attempts[key] = value
	return value.count, nil
}

func TestOwnerSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	service, err := NewService(store, strings.Repeat("s", 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(ctx, "Owner", "strong-password", "wrong", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid setup token: %v", err)
	}
	session, err := service.Setup(ctx, "Owner", "strong-password", strings.Repeat("s", 32), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Username != "owner" || session.User.Role != "owner" {
		t.Fatal("owner normalization failed")
	}
	if !strings.HasPrefix(store.operator.PasswordHash, passwordPrefix) || strings.Contains(store.operator.PasswordHash, "strong-password") {
		t.Fatal("password was not hashed")
	}
	if _, err := service.Setup(ctx, "another", "strong-password", strings.Repeat("s", 32), "127.0.0.1"); !errors.Is(err, ErrInitialized) {
		t.Fatalf("second setup: %v", err)
	}
	if _, exists := store.sessions[session.Token]; exists {
		t.Fatal("plaintext session token persisted")
	}
	loaded, err := service.Authenticate(ctx, session.Token)
	if err != nil || loaded.CSRFToken != session.CSRFToken {
		t.Fatalf("session round trip: %v", err)
	}
	if !ValidCSRF(loaded, session.CSRFToken) || ValidCSRF(loaded, "") || ValidCSRF(loaded, session.Token) {
		t.Fatal("CSRF validation failed")
	}
	if _, err := service.Login(ctx, "owner", "wrong-password", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := service.Login(ctx, "unknown", "strong-password", "127.0.0.1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user: %v", err)
	}
	loggedIn, err := service.Login(ctx, "owner", "strong-password", "127.0.0.1")
	if err != nil || loggedIn.Token == session.Token {
		t.Fatalf("fresh login session: %v", err)
	}
	store.operator.Enabled = false
	if _, err := service.Authenticate(ctx, loggedIn.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled account: %v", err)
	}
	store.operator.Enabled = true
	store.operator.User.Role = "viewer"
	if _, err := service.Authenticate(ctx, loggedIn.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner role: %v", err)
	}
	store.operator.User.Role = "owner"
	if err := service.Logout(ctx, loggedIn.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, loggedIn.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session: %v", err)
	}
	now = now.Add(SessionTTL)
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestLoginLimitSurvivesServiceRestart(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	service, err := NewService(store, strings.Repeat("s", 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if err := service.rateLimit(ctx, "login", "peer", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := NewService(store, strings.Repeat("s", 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Login(ctx, "owner", "password", "another-peer"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("persistent username limit: %v", err)
	}
	now = now.Add(15 * time.Minute)
	if _, err := restarted.Login(ctx, "owner", "password", "another-peer"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("window expiry: %v", err)
	}
}

func TestSetupInputAndPasswordHash(t *testing.T) {
	hash, err := hashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "strong-password") || verifyPassword(hash, "wrong-password") || verifyPassword("malformed", "strong-password") {
		t.Fatal("password verification failed")
	}
	store := newMemoryStore()
	service, err := NewService(store, strings.Repeat("s", 32), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ username, password string }{{"o", "strong-password"}, {"owner", "short"}, {"owner", strings.Repeat("x", 129)}} {
		if _, err := service.Setup(context.Background(), input.username, input.password, strings.Repeat("s", 32), "peer"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
}
