package httptransport

import (
	stdhttp "net/http"

	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (h *Handler) CreateSupplier(c *gin.Context, params apispec.CreateSupplierParams) {
	var request apispec.CreateSupplierRequest
	if !decodeJSON(c, &request) {
		return
	}
	record, hash, proceed := h.lookupIdempotency(c, "create_supplier", params.IdempotencyKey, request)
	if !proceed || h.replay(c, record) {
		return
	}
	models := make([]supplier.ModelInput, 0, len(request.Models))
	for _, model := range request.Models {
		inputPrice, err := decimal.NewFromString(model.InputPrice)
		if err != nil {
			writeError(c, stdhttp.StatusBadRequest, "invalid_input_price", "输入采购价格格式无效")
			return
		}
		outputPrice, err := decimal.NewFromString(model.OutputPrice)
		if err != nil {
			writeError(c, stdhttp.StatusBadRequest, "invalid_output_price", "输出采购价格格式无效")
			return
		}
		models = append(models, supplier.ModelInput{
			Name: model.Model, UpstreamName: model.UpstreamModel,
			InputPrice: inputPrice, OutputPrice: outputPrice, Currency: model.Currency,
		})
	}
	result, err := h.onboarding.CreateSupplier(c.Request.Context(), onboarding.CreateSupplierCommand{
		Code: request.Code, Name: request.Name, UpstreamBaseURL: request.UpstreamBaseUrl,
		UpstreamAPIKey: request.UpstreamApiKey, Models: models, ActorID: "m0-owner",
	})
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	h.writeIdempotent(c, "create_supplier", params.IdempotencyKey, hash, stdhttp.StatusCreated, supplierResponse(result))
}

func (h *Handler) GetSupplier(c *gin.Context, supplierID apispec.SupplierId) {
	result, err := h.onboarding.GetSupplier(c.Request.Context(), uuid.UUID(supplierID))
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, supplierResponse(result))
}
