package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/gin-gonic/gin"
)

type authTestStore struct {
	operator *auth.Operator
	sessions map[string]auth.SessionRecord
}

func (s *authTestStore) AuthInitialized(context.Context) (bool, error) { return s.operator != nil, nil }
func (s *authTestStore) CreateInitialOperator(_ context.Context, operator auth.Operator) (bool, error) {
	if s.operator != nil {
		return false, nil
	}
	s.operator = &operator
	return true, nil
}
func (s *authTestStore) FindOperator(_ context.Context, username string) (*auth.Operator, error) {
	if s.operator == nil || s.operator.User.Username != username {
		return nil, nil
	}
	return s.operator, nil
}
func (s *authTestStore) SaveOperatorSession(_ context.Context, record auth.SessionRecord) error {
	s.sessions[record.TokenHash] = record
	return nil
}
func (s *authTestStore) FindOperatorSession(_ context.Context, hash string) (*auth.SessionRecord, error) {
	record, ok := s.sessions[hash]
	if !ok {
		return nil, nil
	}
	return &record, nil
}
func (s *authTestStore) DeleteOperatorSession(_ context.Context, hash string) error {
	delete(s.sessions, hash)
	return nil
}
func (s *authTestStore) ConsumeAuthAttempt(context.Context, string, time.Time, time.Time) (int32, error) {
	return 1, nil
}

func TestBrowserAuthenticationAndCSRF(t *testing.T) {
	store := &authTestStore{sessions: map[string]auth.SessionRecord{}}
	service, err := auth.NewService(store, strings.Repeat("s", 32), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(sessionAuthentication(service, strings.Repeat("s", 32), false))
	RegisterAuthRoutes(router, service, false)
	router.POST("/api/v1/private", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"actor": OperatorActor(c)}) })
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := func(method, path, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	setupBody := `{"username":"owner","password":"strong-password","setup_token":"` + strings.Repeat("s", 32) + `"}`
	if result := request(http.MethodPost, "/api/v1/auth/setup", setupBody, "http://other.test", "", nil); result.Code != http.StatusForbidden {
		t.Fatalf("cross-origin setup: %d", result.Code)
	}
	setup := request(http.MethodPost, "/api/v1/auth/setup", setupBody, "http://example.test", "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	var session auth.Session
	if err := json.Unmarshal(setup.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := setup.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("session cookie missing")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure || cookie.MaxAge <= 0 {
		t.Fatal("session cookie flags invalid")
	}
	if strings.Contains(setup.Body.String(), cookie.Value) {
		t.Fatal("session token exposed in JSON")
	}
	if result := request(http.MethodPost, "/api/v1/private", "{}", "http://example.test", "", cookie); result.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", result.Code)
	}
	if result := request(http.MethodPost, "/api/v1/private", "{}", "http://other.test", session.CSRFToken, cookie); result.Code != http.StatusForbidden {
		t.Fatalf("cross-origin write: %d", result.Code)
	}
	if result := request(http.MethodPost, "/api/v1/private", "{}", "http://example.test", session.CSRFToken, cookie); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), session.User.ID.String()) {
		t.Fatalf("authorized write: %d", result.Code)
	}
	if result := request(http.MethodGet, "/api/v1/auth/session", "", "", "", cookie); result.Code != http.StatusOK {
		t.Fatalf("session read: %d", result.Code)
	}
	if result := request(http.MethodGet, "/api/v1/auth/unknown", "", "", "", nil); result.Code != http.StatusUnauthorized {
		t.Fatalf("unknown API bypass: %d", result.Code)
	}
	if result := request(http.MethodGet, "/", "", "", "", nil); result.Code != http.StatusOK {
		t.Fatalf("static page: %d", result.Code)
	}
	if result := request(http.MethodPost, "/api/v1/auth/logout", "{}", "http://example.test", session.CSRFToken, cookie); result.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", result.Code)
	}
	if result := request(http.MethodGet, "/api/v1/auth/session", "", "", "", cookie); result.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session: %d", result.Code)
	}

	bearerRequest := httptest.NewRequest(http.MethodPost, "/api/v1/private", nil)
	bearerRequest.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	bearerResponse := httptest.NewRecorder()
	router.ServeHTTP(bearerResponse, bearerRequest)
	if bearerResponse.Code != http.StatusOK {
		t.Fatalf("existing bearer integration: %d", bearerResponse.Code)
	}
}

func TestSecureCookieAndOrigin(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "https://example.test/api/v1/private", nil)
	c.Request.Header.Set("Origin", "https://example.test")
	if !sameOrigin(c, true) {
		t.Fatal("HTTPS same origin rejected")
	}
	c.Request.Header.Set("Origin", "http://example.test")
	if sameOrigin(c, true) {
		t.Fatal("scheme downgrade accepted")
	}
	c.Request.Header.Set("Origin", "https://example.test")
	c.Request.Header.Set("Sec-Fetch-Site", "same-site")
	if sameOrigin(c, true) {
		t.Fatal("sibling-site request accepted")
	}
	(authHandler{cookieSecure: true}).setSession(c, auth.Session{Token: "opaque", ExpiresAt: time.Now().Add(time.Hour)})
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("secure cookie missing")
	}
}
