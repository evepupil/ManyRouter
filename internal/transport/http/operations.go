package httptransport

import (
	stdhttp "net/http"
	"regexp"
	"strconv"

	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var operationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

func RegisterOperationsRoutes(router *gin.Engine, handler *Handler) {
	if handler.operations == nil {
		return
	}
	group := router.Group(managementAPI + "/ops")
	for _, kind := range []string{"sites", "suppliers", "relations", "strategies", "prices", "plans", "sync-operations", "audit"} {
		group.GET("/"+kind, handler.listOperations(kind))
	}
	for _, kind := range []string{"plans", "sync-operations"} {
		group.GET("/"+kind+"/:id", handler.getOperationResource(kind))
	}
	group.POST("/sites", operationMutation[operations.SiteInput](handler, "create_site", stdhttp.StatusCreated))
	group.PUT("/sites/:id", operationMutation[operations.SiteInput](handler, "update_site", stdhttp.StatusOK))
	group.POST("/suppliers", operationMutation[operations.SupplierInput](handler, "create_supplier", stdhttp.StatusCreated))
	group.PUT("/suppliers/:id", operationMutation[operations.SupplierInput](handler, "update_supplier", stdhttp.StatusOK))
	group.POST("/suppliers/:id/credentials", operationMutation[operations.CredentialInput](handler, "rotate_credential", stdhttp.StatusOK))
	group.POST("/suppliers/:id/credentials/cancel", operationMutation[operations.CredentialCancelInput](handler, "cancel_credential", stdhttp.StatusOK))
	group.POST("/deployments", operationMutation[operations.DeploymentInput](handler, "deploy", stdhttp.StatusAccepted))
	group.PUT("/relations/:id", operationMutation[operations.RelationInput](handler, "relation", stdhttp.StatusOK))
	group.PUT("/sites/:id/strategies/:kind", operationMutation[operations.StrategyInput](handler, "strategy", stdhttp.StatusOK))
	group.POST("/prices", operationMutation[operations.PriceInput](handler, "draft_price", stdhttp.StatusCreated))
	group.POST("/prices/:id/publish", operationMutation[operations.PublishInput](handler, "publish_price", stdhttp.StatusOK))
	group.POST("/plans/:id/restore", operationMutation[operations.RestoreInput](handler, "restore", stdhttp.StatusOK))
	group.POST("/sites/:id/sync", handler.syncOperationSite)
}

func (h *Handler) listOperations(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter, ok := operationFilter(c)
		if !ok {
			return
		}
		page, err := h.operations.List(c.Request.Context(), kind, filter)
		if err != nil {
			h.writeApplicationError(c, err)
			return
		}
		c.JSON(stdhttp.StatusOK, page)
	}
}

func (h *Handler) getOperationResource(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := operationID(c)
		if !ok {
			return
		}
		result, err := h.operations.Get(c.Request.Context(), kind, id)
		if err != nil {
			h.writeApplicationError(c, err)
			return
		}
		c.Data(stdhttp.StatusOK, "application/json; charset=utf-8", result)
	}
}

func operationMutation[T any](handler *Handler, kind string, status int) gin.HandlerFunc {
	return func(c *gin.Context) {
		mutation, ok := operationCommand(c, kind)
		if !ok {
			return
		}
		var input T
		if !decodeOperationJSON(c, kind, &input) {
			return
		}
		mutation.Input = input
		handler.executeOperation(c, mutation, status)
	}
}

func (h *Handler) syncOperationSite(c *gin.Context) {
	mutation, ok := operationCommand(c, "sync")
	if !ok {
		return
	}
	if c.Request.ContentLength != 0 {
		var empty struct{}
		if !decodeOperationJSON(c, "sync", &empty) {
			return
		}
	}
	h.executeOperation(c, mutation, stdhttp.StatusAccepted)
}

func (h *Handler) executeOperation(c *gin.Context, mutation operations.Mutation, status int) {
	result, err := h.operations.Execute(c.Request.Context(), mutation)
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.Data(status, "application/json; charset=utf-8", result)
}

func operationCommand(c *gin.Context, kind string) (operations.Mutation, bool) {
	key := c.GetHeader("Idempotency-Key")
	if !operationKeyPattern.MatchString(key) {
		writeError(c, stdhttp.StatusBadRequest, "invalid_idempotency_key", "请求编号需为 8 至 128 位字母、数字或 ._:-")
		return operations.Mutation{}, false
	}
	mutation := operations.Mutation{Kind: kind, Actor: OperatorActor(c), Key: key, StrategyKind: c.Param("kind")}
	if c.Param("id") != "" {
		id, ok := operationID(c)
		if !ok {
			return operations.Mutation{}, false
		}
		mutation.ID = id
	}
	return mutation, true
}

func operationID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_identifier", "业务对象编号无效")
		return uuid.Nil, false
	}
	return id, true
}

func operationFilter(c *gin.Context) (operations.Filter, bool) {
	filter := operations.Filter{Query: c.Query("q")}
	for _, field := range []struct {
		name   string
		target *uuid.UUID
	}{{"site_id", &filter.SiteID}, {"supplier_id", &filter.SupplierID}} {
		if value := c.Query(field.name); value != "" {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				writeError(c, stdhttp.StatusBadRequest, "invalid_filter", "筛选中的站点或供应商编号无效")
				return operations.Filter{}, false
			}
			*field.target = id
		}
	}
	for _, field := range []struct {
		name   string
		target *int
	}{{"limit", &filter.Limit}, {"offset", &filter.Offset}} {
		if value := c.Query(field.name); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 0 {
				writeError(c, stdhttp.StatusBadRequest, "invalid_filter", "分页参数需为非负整数")
				return operations.Filter{}, false
			}
			*field.target = number
		}
	}
	return filter, true
}
