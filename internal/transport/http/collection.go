package httptransport

import (
	stdhttp "net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterCollectionRoutes(router *gin.Engine, handler *Handler) {
	if handler.collection == nil {
		return
	}
	group := router.Group(managementAPI + "/ops")
	group.GET("/collection-status", handler.listCollectionStatus)
	group.POST("/collection-runs", handler.runCollection)
}

func (handler *Handler) listCollectionStatus(c *gin.Context) {
	var siteID *uuid.UUID
	if raw := c.Query("site_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil || parsed == uuid.Nil {
			writeError(c, stdhttp.StatusBadRequest, "invalid_filter", "站点筛选编号无效")
			return
		}
		siteID = &parsed
	}
	result, err := handler.collection.ListStatus(c.Request.Context(), siteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, map[string]any{"items": result})
}

func (handler *Handler) runCollection(c *gin.Context) {
	var request struct {
		SiteID uuid.UUID `json:"site_id"`
	}
	if !decodeJSON(c, &request) {
		return
	}
	if request.SiteID == uuid.Nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_request", "必须选择需要采集的站点")
		return
	}
	key := c.GetHeader("Idempotency-Key")
	record, hash, proceed := handler.lookupIdempotency(c, "collect_measurements", key, request)
	if !proceed || handler.replay(c, record) {
		return
	}
	result, err := handler.collection.CollectSite(c.Request.Context(), request.SiteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "collect_measurements", key, hash, stdhttp.StatusOK, result)
}
