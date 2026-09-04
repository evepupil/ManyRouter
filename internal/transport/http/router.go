package httptransport

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	stdhttp "net/http"
	"strings"

	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	requestIDKey  = "request_id"
	maxBodyBytes  = 1 << 20
	managementAPI = "/api/v1"
)

func NewRouter(handler *Handler, operatorToken string, logger *slog.Logger) (*gin.Engine, error) {
	if handler == nil || logger == nil {
		return nil, errors.New("router dependencies are required")
	}
	if len(operatorToken) < 32 {
		return nil, errors.New("operator token must contain at least 32 characters")
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(requestIDMiddleware())
	router.Use(securityHeaders())
	router.Use(bodyLimit())
	router.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "panic recovered", "request_id", requestID(c), "panic_type", recoveredType(recovered))
		writeError(c, stdhttp.StatusInternalServerError, "internal_error", "请求暂时无法完成")
	}))
	router.Use(operatorAuthentication(operatorToken))
	apispec.RegisterHandlersWithOptions(router, handler, apispec.GinServerOptions{
		BaseURL: managementAPI,
		ErrorHandler: func(c *gin.Context, _ error, statusCode int) {
			writeError(c, statusCode, "invalid_parameter", "请求参数格式无效")
		},
	})
	return router, nil
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	}
}

func bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = stdhttp.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}

func operatorAuthentication(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == managementAPI+"/healthz" {
			c.Next()
			return
		}
		provided := bearerToken(c.GetHeader("Authorization"))
		if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			writeError(c, stdhttp.StatusUnauthorized, "unauthorized", "运营凭证无效")
			c.Abort()
			return
		}
		c.Next()
	}
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func requestID(c *gin.Context) string {
	value, exists := c.Get(requestIDKey)
	if !exists {
		return "unknown"
	}
	id, ok := value.(string)
	if !ok {
		return "unknown"
	}
	return id
}

func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, apispec.ErrorResponse{Code: code, Message: message, RequestId: requestID(c)})
}

func recoveredType(value any) string {
	switch value.(type) {
	case error:
		return "error"
	case string:
		return "string"
	default:
		return "unknown"
	}
}
