package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnauthorized = errors.New("authentication required")
	ErrForbidden    = errors.New("operation not permitted")
	ErrInitialized  = errors.New("owner already initialized")
	ErrInvalidInput = errors.New("invalid authentication input")
	ErrRateLimited  = errors.New("too many authentication attempts")
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,79}$`)
)

const SessionTTL = 12 * time.Hour

type User struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	Role     string    `json:"role"`
}

type Operator struct {
	User         User
	PasswordHash string
	Enabled      bool
	CreatedAt    time.Time
}

type SessionRecord struct {
	TokenHash string
	User      User
	CSRFHash  string
	ExpiresAt time.Time
	CreatedAt time.Time
	Enabled   bool
}

type Session struct {
	User      User      `json:"user"`
	CSRFToken string    `json:"csrf_token"`
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"-"`
}

type Store interface {
	AuthInitialized(context.Context) (bool, error)
	CreateInitialOperator(context.Context, Operator) (bool, error)
	FindOperator(context.Context, string) (*Operator, error)
	SaveOperatorSession(context.Context, SessionRecord) error
	FindOperatorSession(context.Context, string) (*SessionRecord, error)
	DeleteOperatorSession(context.Context, string) error
	ConsumeAuthAttempt(context.Context, string, time.Time, time.Time) (int32, error)
}

type Service struct {
	store             Store
	setupTokenHash    [32]byte
	now               func() time.Time
	dummyPasswordHash string
	passwordSlots     chan struct{}
}

func NewService(store Store, setupToken string, now func() time.Time) (*Service, error) {
	if store == nil || len(setupToken) < 32 || now == nil {
		return nil, errors.New("authentication dependencies are required")
	}
	dummy, err := hashPassword(uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &Service{store: store, setupTokenHash: sha256.Sum256([]byte(setupToken)), now: now, dummyPasswordHash: dummy, passwordSlots: make(chan struct{}, 2)}, nil
}

func (s *Service) Initialized(ctx context.Context) (bool, error) {
	return s.store.AuthInitialized(ctx)
}

func (s *Service) Setup(ctx context.Context, username, password, setupToken, peer string) (Session, error) {
	if err := s.rateLimit(ctx, "setup", peer, ""); err != nil {
		return Session{}, err
	}
	provided := sha256.Sum256([]byte(setupToken))
	if subtle.ConstantTimeCompare(provided[:], s.setupTokenHash[:]) != 1 {
		return Session{}, ErrUnauthorized
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) || len(password) < 12 || len(password) > 128 {
		return Session{}, ErrInvalidInput
	}
	initialized, err := s.store.AuthInitialized(ctx)
	if err != nil {
		return Session{}, err
	}
	if initialized {
		return Session{}, ErrInitialized
	}
	passwordHash, err := withPasswordSlot(ctx, s.passwordSlots, func() (string, error) { return hashPassword(password) })
	if err != nil {
		return Session{}, err
	}
	operator := Operator{User: User{ID: uuid.New(), Username: username, Role: "owner"}, PasswordHash: passwordHash, Enabled: true, CreatedAt: s.now().UTC()}
	created, err := s.store.CreateInitialOperator(ctx, operator)
	if err != nil {
		return Session{}, err
	}
	if !created {
		return Session{}, ErrInitialized
	}
	return s.newSession(ctx, operator.User)
}

func (s *Service) Login(ctx context.Context, username, password, peer string) (Session, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) > 80 || len(password) > 128 {
		return Session{}, ErrUnauthorized
	}
	if err := s.rateLimit(ctx, "login", peer, username); err != nil {
		return Session{}, err
	}
	operator, err := s.store.FindOperator(ctx, username)
	if err != nil {
		return Session{}, err
	}
	encoded := s.dummyPasswordHash
	if operator != nil {
		encoded = operator.PasswordHash
	}
	valid, err := withPasswordSlot(ctx, s.passwordSlots, func() (bool, error) { return verifyPassword(encoded, password), nil })
	if err != nil {
		return Session{}, err
	}
	if operator == nil || !valid || !operator.Enabled || operator.User.Role != "owner" {
		return Session{}, ErrUnauthorized
	}
	return s.newSession(ctx, operator.User)
}

func withPasswordSlot[T any](ctx context.Context, slots chan struct{}, action func() (T, error)) (T, error) {
	var zero T
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
		return action()
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrUnauthorized
	}
	record, err := s.store.FindOperatorSession(ctx, hashToken(token))
	if err != nil {
		return Session{}, err
	}
	if record == nil || !record.Enabled || !record.ExpiresAt.After(s.now().UTC()) {
		return Session{}, ErrUnauthorized
	}
	if record.User.Role != "owner" {
		return Session{}, ErrForbidden
	}
	csrf := csrfToken(token)
	if !constantEqual(hashToken(csrf), record.CSRFHash) {
		return Session{}, ErrUnauthorized
	}
	return Session{User: record.User, CSRFToken: csrf, Token: token, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if !validToken(token) {
		return ErrUnauthorized
	}
	return s.store.DeleteOperatorSession(ctx, hashToken(token))
}

func (s *Service) newSession(ctx context.Context, user User) (Session, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	now := s.now().UTC()
	session := Session{User: user, Token: token, CSRFToken: csrfToken(token), ExpiresAt: now.Add(SessionTTL)}
	err := s.store.SaveOperatorSession(ctx, SessionRecord{TokenHash: hashToken(token), User: user, CSRFHash: hashToken(session.CSRFToken), ExpiresAt: session.ExpiresAt, CreatedAt: now, Enabled: true})
	return session, err
}

func (s *Service) rateLimit(ctx context.Context, action, peer, username string) error {
	now := s.now().UTC()
	keys := []struct {
		key   string
		limit int32
	}{{action + ":peer:" + peer, 30}}
	if action == "login" {
		keys = append(keys, struct {
			key   string
			limit int32
		}{action + ":user:" + username, 10})
	}
	for _, entry := range keys {
		attempts, err := s.store.ConsumeAuthAttempt(ctx, hashToken(entry.key), now, now.Add(-15*time.Minute))
		if err != nil {
			return err
		}
		if attempts > entry.limit {
			return ErrRateLimited
		}
	}
	return nil
}

func ValidCSRF(session Session, provided string) bool {
	return provided != "" && constantEqual(session.CSRFToken, provided)
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func csrfToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("manyrouter-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == 32
}
