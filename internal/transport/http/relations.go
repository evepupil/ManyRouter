package httptransport

import (
	stdhttp "net/http"

	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (h *Handler) CreateSiteSupplier(c *gin.Context, params apispec.CreateSiteSupplierParams) {
	var request apispec.CreateSiteSupplierRequest
	if !decodeJSON(c, &request) {
		return
	}
	record, hash, proceed := h.lookupIdempotency(c, "create_site_supplier", params.IdempotencyKey, request)
	if !proceed || h.replay(c, record) {
		return
	}
	ratio, err := decimal.NewFromString(request.SaleRatio)
	if err != nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_sale_ratio", "销售倍率格式无效")
		return
	}
	visible := true
	if request.Visible != nil {
		visible = *request.Visible
	}
	relation, _, err := h.onboarding.CreateRelation(c.Request.Context(), onboarding.CreateRelationCommand{
		SiteID: uuid.UUID(request.SiteId), SupplierID: uuid.UUID(request.SupplierId),
		GroupDisplayName: request.GroupDisplayName, SaleRatio: ratio, Visible: visible, ActorID: "m0-owner",
	})
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	h.writeIdempotent(c, "create_site_supplier", params.IdempotencyKey, hash, stdhttp.StatusCreated, siteSupplierResponse(relation))
}

func (h *Handler) GetSiteSupplier(c *gin.Context, relationID apispec.RelationId) {
	result, err := h.onboarding.GetRelation(c.Request.Context(), uuid.UUID(relationID))
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, siteSupplierResponse(result))
}

func (h *Handler) SyncSiteSupplier(c *gin.Context, relationID apispec.RelationId, params apispec.SyncSiteSupplierParams) {
	request := struct {
		RelationID uuid.UUID `json:"relation_id"`
	}{RelationID: uuid.UUID(relationID)}
	record, hash, proceed := h.lookupIdempotency(c, "sync_site_supplier", params.IdempotencyKey, request)
	if !proceed || h.replay(c, record) {
		return
	}
	operation, err := h.reconciliation.RequestSync(c.Request.Context(), request.RelationID)
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	h.writeIdempotent(c, "sync_site_supplier", params.IdempotencyKey, hash, stdhttp.StatusAccepted, syncOperationResponse(operation))
}

func (h *Handler) GetSyncOperation(c *gin.Context, operationID apispec.OperationId) {
	operation, err := h.reconciliation.GetOperation(c.Request.Context(), uuid.UUID(operationID))
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, syncOperationResponse(operation))
}
