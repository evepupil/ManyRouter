package httptransport

import (
	stdhttp "net/http"
	"strconv"
	"strings"

	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterCatalogRoutes(router *gin.Engine, handler *Handler) {
	if handler.catalog == nil {
		return
	}
	operator := router.Group(managementAPI + "/ops")
	operator.GET("/site-product-tokens", handler.listProductTokens)
	operator.POST("/sites/:id/product-tokens", handler.createProductToken)
	operator.POST("/sites/:id/product-tokens/:token_id/revoke", handler.revokeProductToken)
	public := router.Group(managementAPI + "/site")
	public.GET("/capabilities", handler.catalogCapabilities)
	public.GET("/products", handler.catalogProducts)
}

func (handler *Handler) listProductTokens(c *gin.Context) {
	siteID, ok := requiredSiteQuery(c)
	if !ok {
		return
	}
	tokens, err := handler.catalog.ListTokens(c.Request.Context(), siteID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, map[string]any{"items": tokens})
}

func (handler *Handler) createProductToken(c *gin.Context) {
	siteID, ok := operationID(c)
	if !ok {
		return
	}
	request := struct {
		Reason string `json:"reason"`
	}{}
	if !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	idempotencyInput := struct {
		SiteID uuid.UUID `json:"site_id"`
		Reason string    `json:"reason"`
	}{SiteID: siteID, Reason: request.Reason}
	record, hash, proceed := handler.lookupIdempotency(c, "create_site_product_token", key, idempotencyInput)
	if !proceed || handler.replay(c, record) {
		return
	}
	issued, err := handler.catalog.CreateToken(c.Request.Context(), siteID, request.Reason, OperatorActor(c))
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "create_site_product_token", key, hash, stdhttp.StatusCreated, issued)
}

func (handler *Handler) revokeProductToken(c *gin.Context) {
	siteID, ok := operationID(c)
	if !ok {
		return
	}
	tokenID, err := uuid.Parse(c.Param("token_id"))
	if err != nil || tokenID == uuid.Nil {
		writeError(c, stdhttp.StatusBadRequest, "invalid_identifier", "站点产品令牌编号无效")
		return
	}
	request := struct {
		Reason string `json:"reason"`
	}{}
	if !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	idempotencyInput := struct {
		SiteID  uuid.UUID `json:"site_id"`
		TokenID uuid.UUID `json:"token_id"`
		Reason  string    `json:"reason"`
	}{SiteID: siteID, TokenID: tokenID, Reason: request.Reason}
	record, hash, proceed := handler.lookupIdempotency(c, "revoke_site_product_token", key, idempotencyInput)
	if !proceed || handler.replay(c, record) {
		return
	}
	if err := handler.catalog.RevokeToken(c.Request.Context(), siteID, tokenID, request.Reason, OperatorActor(c)); err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	response := map[string]string{"status": "revoked"}
	handler.writeIdempotent(c, "revoke_site_product_token", key, hash, stdhttp.StatusOK, response)
}

func (handler *Handler) catalogCapabilities(c *gin.Context) {
	siteID, ok := handler.authenticateCatalog(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "private, max-age=60")
	c.JSON(stdhttp.StatusOK, map[string]any{
		"contract_version": domaincatalog.ContractVersion,
		"site_id":          siteID,
	})
}

func (handler *Handler) catalogProducts(c *gin.Context) {
	token := bearerToken(c.GetHeader("Authorization"))
	snapshot, err := handler.catalog.GetProducts(c.Request.Context(), token)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	etag := strconv.Quote(snapshot.ContentHash)
	if strings.TrimSpace(c.GetHeader("If-None-Match")) == etag {
		c.Header("ETag", etag)
		c.Header("Cache-Control", "private, max-age=60")
		c.Status(stdhttp.StatusNotModified)
		return
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=60")
	c.JSON(stdhttp.StatusOK, snapshot)
}

func (handler *Handler) authenticateCatalog(c *gin.Context) (uuid.UUID, bool) {
	siteID, err := handler.catalog.Authenticate(c.Request.Context(), bearerToken(c.GetHeader("Authorization")))
	if err != nil {
		handler.writeApplicationError(c, err)
		return uuid.Nil, false
	}
	return siteID, true
}
