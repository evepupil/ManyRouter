package httptransport

import (
	stdhttp "net/http"

	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) CreateSite(c *gin.Context, params apispec.CreateSiteParams) {
	var request apispec.CreateSiteRequest
	if !decodeJSON(c, &request) {
		return
	}
	record, hash, proceed := h.lookupIdempotency(c, "create_site", params.IdempotencyKey, request)
	if !proceed || h.replay(c, record) {
		return
	}
	result, err := h.onboarding.CreateSite(c.Request.Context(), onboarding.CreateSiteCommand{
		Code: request.Code, Name: request.Name, NewAPIBaseURL: request.NewApiBaseUrl,
		NewAPIAccessToken: request.NewApiAccessToken, ActorID: "m0-owner",
	})
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	h.writeIdempotent(c, "create_site", params.IdempotencyKey, hash, stdhttp.StatusCreated, siteResponse(result))
}

func (h *Handler) GetSite(c *gin.Context, siteID apispec.SiteId) {
	result, err := h.onboarding.GetSite(c.Request.Context(), uuid.UUID(siteID))
	if err != nil {
		h.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, siteResponse(result))
}
