package httptransport

import (
	stdhttp "net/http"
	"strings"

	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterEvaluationRoutes(router *gin.Engine, handler *Handler) {
	if handler.evaluation == nil {
		return
	}
	group := router.Group(managementAPI + "/ops")
	group.GET("/evaluation-runs", handler.listEvaluationRuns)
	group.GET("/evaluation-runs/:id", handler.getEvaluationRun)
	group.POST("/evaluation-runs", handler.requestEvaluationRun)
	group.POST("/evaluation-runs/:id/reference", handler.promoteEvaluationReference)
}

func (handler *Handler) listEvaluationRuns(c *gin.Context) {
	base, ok := operationFilter(c)
	if !ok {
		return
	}
	filter := evaluationapp.RunFilter{
		Model: strings.TrimSpace(c.Query("model")), Purpose: evaluationapp.Purpose(strings.TrimSpace(c.Query("purpose"))),
		Limit: base.Limit, Offset: base.Offset,
	}
	if base.SiteID != uuid.Nil {
		filter.SiteID = &base.SiteID
	}
	if base.SupplierID != uuid.Nil {
		filter.SupplierID = &base.SupplierID
	}
	result, err := handler.evaluation.ListRuns(c.Request.Context(), filter)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, result)
}

func (handler *Handler) getEvaluationRun(c *gin.Context) {
	runID, ok := operationID(c)
	if !ok {
		return
	}
	result, err := handler.evaluation.GetRun(c.Request.Context(), runID)
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, result)
}

func (handler *Handler) requestEvaluationRun(c *gin.Context) {
	var request struct {
		SupplierID uuid.UUID `json:"supplier_id"`
		Model      string    `json:"model"`
		Purpose    string    `json:"purpose"`
		TargetKind string    `json:"target_kind,omitempty"`
		Reason     string    `json:"reason"`
	}
	if !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	record, hash, proceed := handler.lookupIdempotency(c, "request_evaluation", key, request)
	if !proceed || handler.replay(c, record) {
		return
	}
	result, err := handler.evaluation.RequestRun(c.Request.Context(), evaluationapp.RunCommand{
		SupplierID: request.SupplierID, Model: request.Model, Purpose: evaluationapp.Purpose(request.Purpose),
		TargetKind: evaluationapp.TargetKind(request.TargetKind), Reason: request.Reason, Actor: OperatorActor(c),
		RequestKey: key, RequestHash: hash,
	})
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	handler.writeIdempotent(c, "request_evaluation", key, hash, stdhttp.StatusAccepted, result)
}

func (handler *Handler) promoteEvaluationReference(c *gin.Context) {
	runID, ok := operationID(c)
	if !ok {
		return
	}
	var request struct {
		Trust     string `json:"trust"`
		Reason    string `json:"reason"`
		ValidDays int    `json:"valid_days"`
	}
	if !decodeJSON(c, &request) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	idempotencyInput := struct {
		RunID     uuid.UUID `json:"run_id"`
		Trust     string    `json:"trust"`
		Reason    string    `json:"reason"`
		ValidDays int       `json:"valid_days"`
	}{RunID: runID, Trust: request.Trust, Reason: request.Reason, ValidDays: request.ValidDays}
	record, hash, proceed := handler.lookupIdempotency(c, "promote_evaluation_reference", key, idempotencyInput)
	if !proceed || handler.replay(c, record) {
		return
	}
	reference, err := handler.evaluation.PromoteReference(c.Request.Context(), evaluationapp.ReferenceCommand{
		RunID: runID, Trust: domainevaluation.ReferenceTrust(request.Trust), Reason: request.Reason,
		Actor: OperatorActor(c), ValidDays: request.ValidDays, RequestKey: key, RequestHash: hash,
	})
	if err != nil {
		handler.writeApplicationError(c, err)
		return
	}
	response := map[string]any{
		"id": reference.ID, "model": reference.Source.Model,
		"supplier_id": reference.Source.SupplierID, "trust": reference.Trust,
		"expires_at": reference.ExpiresAt,
	}
	handler.writeIdempotent(c, "promote_evaluation_reference", key, hash, stdhttp.StatusCreated, response)
}
