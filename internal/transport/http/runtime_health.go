package httptransport

import (
	stdhttp "net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterRuntimeHealthRoutes(router *gin.Engine, handler *Handler) {
	if handler.runtimeHealth == nil {
		return
	}
	group := router.Group(managementAPI + "/ops/runtime-health")
	group.GET("", handler.getRuntimeHealth)
	group.GET("/:site_id", handler.getRuntimeSiteHealth)
	group.POST("/:site_id/check", handler.checkRuntimeSiteHealth)
	router.GET("/metrics", handler.getMetrics)
}

func (handler *Handler) getRuntimeHealth(c *gin.Context) {
	result, err := handler.runtimeHealth.Summary(c.Request.Context())
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, result)
}

func (handler *Handler) getRuntimeSiteHealth(c *gin.Context) {
	siteID, ok := runtimeSiteID(c)
	if !ok {
		return
	}
	result, err := handler.runtimeHealth.Detail(c.Request.Context(), siteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, result)
}

func (handler *Handler) checkRuntimeSiteHealth(c *gin.Context) {
	siteID, ok := runtimeSiteID(c)
	if !ok {
		return
	}
	var request struct{}
	if c.Request.ContentLength != 0 && !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	idempotencyInput := struct {
		SiteID uuid.UUID `json:"site_id"`
	}{SiteID: siteID}
	record, hash, proceed := handler.lookupIdempotency(c, "check_runtime_site", key, idempotencyInput)
	if !proceed || handler.replay(c, record) {
		return
	}
	result, err := handler.runtimeHealth.Check(c.Request.Context(), siteID, OperatorActor(c))
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "check_runtime_site", key, hash, stdhttp.StatusOK, result)
}

func (handler *Handler) getMetrics(c *gin.Context) {
	payload, err := handler.runtimeHealth.Prometheus(c.Request.Context())
	if err != nil {
		handler.logger.WarnContext(c.Request.Context(), "runtime metrics are partial", "request_id", requestID(c), "error", err)
	}
	c.Data(stdhttp.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(payload))
}

func runtimeSiteID(c *gin.Context) (uuid.UUID, bool) {
	siteID, err := uuid.Parse(c.Param("site_id"))
	if err != nil || siteID == uuid.Nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_site_id", "站点编号无效")
		return uuid.Nil, false
	}
	return siteID, true
}
