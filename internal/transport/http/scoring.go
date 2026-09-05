package httptransport

import (
	stdhttp "net/http"
	"strings"

	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterScoringRoutes(router *gin.Engine, handler *Handler) {
	if handler.scoring == nil {
		return
	}
	group := router.Group(managementAPI + "/ops")
	group.GET("/score-insights", handler.listScoreInsights)
	group.POST("/score-runs", handler.refreshScores)
}

func (handler *Handler) listScoreInsights(c *gin.Context) {
	base, ok := operationFilter(c)
	if !ok {
		return
	}
	filter := scoringapp.InsightFilter{
		Model: strings.TrimSpace(c.Query("model")), Limit: base.Limit, Offset: base.Offset,
	}
	if base.SiteID != uuid.Nil {
		filter.SiteID = &base.SiteID
	}
	if base.SupplierID != uuid.Nil {
		filter.SupplierID = &base.SupplierID
	}
	result, err := handler.scoring.ListInsights(c.Request.Context(), filter)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, result)
}

func (handler *Handler) refreshScores(c *gin.Context) {
	var request struct{}
	if c.Request.ContentLength != 0 && !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	record, hash, proceed := handler.lookupIdempotency(c, "refresh_shadow_scores", key, request)
	if !proceed || handler.replay(c, record) {
		return
	}
	if err := handler.scoring.Refresh(c.Request.Context()); err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	response := map[string]string{"status": "completed"}
	handler.writeIdempotent(c, "refresh_shadow_scores", key, hash, stdhttp.StatusOK, response)
}
