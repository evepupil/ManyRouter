package httptransport

import (
	stdhttp "net/http"
	"strings"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterAutomationRoutes(router *gin.Engine, handler *Handler) {
	if handler.automation == nil {
		return
	}
	group := router.Group(managementAPI + "/ops")
	group.GET("/automation-settings", handler.listAutomationSettings)
	group.PUT("/sites/:id/automation/:kind", handler.updateAutomationSetting)
	group.GET("/automation-runs", handler.listAutomationRuns)
	group.POST("/automation-runs", handler.runAutomation)
}

func (handler *Handler) listAutomationSettings(c *gin.Context) {
	siteID, ok := requiredSiteQuery(c)
	if !ok {
		return
	}
	settings, err := handler.automation.ListSettings(c.Request.Context(), siteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, map[string]any{"items": settings})
}

func (handler *Handler) updateAutomationSetting(c *gin.Context) {
	siteID, ok := operationID(c)
	if !ok {
		return
	}
	request := struct {
		Mode    automationapp.Mode `json:"mode"`
		Version int64              `json:"version"`
		Reason  string             `json:"reason"`
	}{}
	if !decodeJSON(c, &request) {
		return
	}
	kind := strings.TrimSpace(c.Param("kind"))
	key := c.GetHeader("Idempotency-Key")
	idempotencyInput := struct {
		SiteID  uuid.UUID          `json:"site_id"`
		Kind    string             `json:"strategy_kind"`
		Mode    automationapp.Mode `json:"mode"`
		Version int64              `json:"version"`
		Reason  string             `json:"reason"`
	}{SiteID: siteID, Kind: kind, Mode: request.Mode, Version: request.Version, Reason: request.Reason}
	record, hash, proceed := handler.lookupIdempotency(c, "update_automation_setting", key, idempotencyInput)
	if !proceed || handler.replay(c, record) {
		return
	}
	setting, err := handler.automation.UpdateSetting(c.Request.Context(), automationapp.UpdateSettingCommand{
		SiteID: siteID, StrategyKind: kind, Mode: request.Mode, Version: request.Version,
		Reason: request.Reason, Actor: OperatorActor(c),
	})
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "update_automation_setting", key, hash, stdhttp.StatusOK, setting)
}

func (handler *Handler) listAutomationRuns(c *gin.Context) {
	base, ok := operationFilter(c)
	if !ok {
		return
	}
	filter := automationapp.RunFilter{Limit: base.Limit, Offset: base.Offset}
	if base.SiteID != uuid.Nil {
		filter.SiteID = &base.SiteID
	}
	page, err := handler.automation.ListRuns(c.Request.Context(), filter)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, page)
}

func (handler *Handler) runAutomation(c *gin.Context) {
	request := struct {
		SiteID uuid.UUID `json:"site_id"`
	}{}
	if !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	record, hash, proceed := handler.lookupIdempotency(c, "run_fixed_auto_automation", key, request)
	if !proceed || handler.replay(c, record) {
		return
	}
	run, err := handler.automation.ProcessLatest(c.Request.Context(), request.SiteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "run_fixed_auto_automation", key, hash, stdhttp.StatusAccepted, run)
}

func requiredSiteQuery(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Query("site_id"))
	if err != nil || id == uuid.Nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_filter", "请选择有效站点")
		return uuid.Nil, false
	}
	return id, true
}
