package httptransport

import (
	"crypto/subtle"
	"errors"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/gin-gonic/gin"
)

const sessionCookie = "manyrouter_session"
const operatorActorKey = "operator_actor"

type authHandler struct {
	service      *auth.Service
	cookieSecure bool
}

func RegisterAuthRoutes(router *gin.Engine, service *auth.Service, cookieSecure bool) {
	handler := authHandler{service: service, cookieSecure: cookieSecure}
	group := router.Group(managementAPI + "/auth")
	group.GET("/status", handler.status)
	group.POST("/setup", handler.setup)
	group.POST("/login", handler.login)
	group.GET("/session", handler.session)
	group.POST("/logout", handler.logout)
}

func OperatorActor(c *gin.Context) string {
	actor, exists := c.Get(operatorActorKey)
	if exists {
		if value, ok := actor.(string); ok && value != "" {
			return value
		}
	}
	return "deployment-owner"
}

func sessionAuthentication(service *auth.Service, operatorToken string, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if (!strings.HasPrefix(path, "/api/") && path != "/api" && path != "/metrics") || publicAuthEndpoint(c.Request.Method, path) {
			c.Next()
			return
		}
		if header := c.GetHeader("Authorization"); header != "" {
			provided := bearerToken(header)
			if len(provided) != len(operatorToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(operatorToken)) != 1 {
				authError(c, auth.ErrUnauthorized)
				return
			}
			c.Set(operatorActorKey, "deployment-owner")
			c.Next()
			return
		}
		session, err := readSession(c, service)
		if err != nil {
			authError(c, err)
			return
		}
		if isWriteMethod(c.Request.Method) && (!sameOrigin(c, secure) || !auth.ValidCSRF(session, c.GetHeader("X-CSRF-Token"))) {
			authError(c, auth.ErrForbidden)
			return
		}
		c.Set(operatorActorKey, session.User.ID.String())
		c.Next()
	}
}

func publicAuthEndpoint(method, path string) bool {
	if siteCatalogEndpoint(method, path) {
		return true
	}
	if method == stdhttp.MethodGet && path == managementAPI+"/healthz" {
		return true
	}
	if method == stdhttp.MethodGet {
		return path == managementAPI+"/auth/status" || path == managementAPI+"/auth/session"
	}
	if method == stdhttp.MethodPost {
		return path == managementAPI+"/auth/setup" || path == managementAPI+"/auth/login" || path == managementAPI+"/auth/logout"
	}
	return false
}

func (h authHandler) status(c *gin.Context) {
	initialized, err := h.service.Initialized(c.Request.Context())
	if err != nil {
		authError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, gin.H{"initialized": initialized})
}

func (h authHandler) setup(c *gin.Context) {
	var request struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		SetupToken string `json:"setup_token"`
	}
	if !h.validAuthBody(c) || !decodeJSON(c, &request) {
		return
	}
	session, err := h.service.Setup(c.Request.Context(), request.Username, request.Password, request.SetupToken, c.RemoteIP())
	if err != nil {
		authError(c, err)
		return
	}
	h.setSession(c, session)
	c.JSON(stdhttp.StatusCreated, session)
}

func (h authHandler) login(c *gin.Context) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !h.validAuthBody(c) || !decodeJSON(c, &request) {
		return
	}
	session, err := h.service.Login(c.Request.Context(), request.Username, request.Password, c.RemoteIP())
	if err != nil {
		authError(c, err)
		return
	}
	h.setSession(c, session)
	c.JSON(stdhttp.StatusOK, session)
}

func (h authHandler) session(c *gin.Context) {
	session, err := readSession(c, h.service)
	if err != nil {
		authError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, session)
}

func (h authHandler) logout(c *gin.Context) {
	session, err := readSession(c, h.service)
	if err != nil {
		authError(c, err)
		return
	}
	if !sameOrigin(c, h.cookieSecure) || !auth.ValidCSRF(session, c.GetHeader("X-CSRF-Token")) {
		authError(c, auth.ErrForbidden)
		return
	}
	if err := h.service.Logout(c.Request.Context(), session.Token); err != nil {
		authError(c, err)
		return
	}
	stdhttp.SetCookie(c.Writer, &stdhttp.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: stdhttp.SameSiteStrictMode, MaxAge: -1})
	c.Status(stdhttp.StatusNoContent)
}

func (h authHandler) validAuthBody(c *gin.Context) bool {
	if !sameOrigin(c, h.cookieSecure) {
		authError(c, auth.ErrForbidden)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		authError(c, auth.ErrInvalidInput)
		return false
	}
	return true
}

func (h authHandler) setSession(c *gin.Context, session auth.Session) {
	stdhttp.SetCookie(c.Writer, &stdhttp.Cookie{Name: sessionCookie, Value: session.Token, Path: "/", HttpOnly: true, Secure: h.cookieSecure, SameSite: stdhttp.SameSiteStrictMode, MaxAge: int(auth.SessionTTL.Seconds()), Expires: session.ExpiresAt})
}

func readSession(c *gin.Context, service *auth.Service) (auth.Session, error) {
	token, err := c.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, auth.ErrUnauthorized
	}
	return service.Authenticate(c.Request.Context(), token)
}

func sameOrigin(c *gin.Context, secure bool) bool {
	if fetchSite := c.GetHeader("Sec-Fetch-Site"); fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	origin := c.GetHeader("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	if secure {
		scheme = "https"
	}
	return parsed.Scheme == scheme && strings.EqualFold(parsed.Host, c.Request.Host)
}

func isWriteMethod(method string) bool {
	return method != stdhttp.MethodGet && method != stdhttp.MethodHead && method != stdhttp.MethodOptions
}

func authError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(c, stdhttp.StatusUnauthorized, "unauthorized", "账号、密码或登录状态无效")
	case errors.Is(err, auth.ErrForbidden):
		writeError(c, stdhttp.StatusForbidden, "forbidden", "当前操作未通过身份或来源检查")
	case errors.Is(err, auth.ErrInitialized):
		writeError(c, stdhttp.StatusConflict, "already_initialized", "所有者账号已经创建")
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(c, stdhttp.StatusBadRequest, "invalid_auth_input", "账号需为 3 至 80 位字母数字或 ._-，密码需为 12 至 128 字节")
	case errors.Is(err, auth.ErrRateLimited):
		c.Header("Retry-After", "900")
		writeError(c, stdhttp.StatusTooManyRequests, "rate_limited", "登录尝试过于频繁，请稍后重试")
	default:
		writeError(c, stdhttp.StatusInternalServerError, "internal_error", "登录服务暂时不可用")
	}
}
