package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	stdhttp "net/http"

	"github.com/evepupil/ManyRouter/internal/application/collection"
	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type onboardingUseCases interface {
	CreateSite(context.Context, onboarding.CreateSiteCommand) (site.Site, error)
	GetSite(context.Context, uuid.UUID) (site.Site, error)
	CreateSupplier(context.Context, onboarding.CreateSupplierCommand) (supplier.Supplier, error)
	GetSupplier(context.Context, uuid.UUID) (supplier.Supplier, error)
	CreateRelation(context.Context, onboarding.CreateRelationCommand) (routing.Relation, routing.Plan, error)
	GetRelation(context.Context, uuid.UUID) (routing.Relation, error)
}

type reconciliationUseCases interface {
	RequestSync(context.Context, uuid.UUID) (reconciliation.Operation, error)
	GetOperation(context.Context, uuid.UUID) (reconciliation.Operation, error)
}

type operationsUseCases interface {
	List(context.Context, string, operations.Filter) (operations.Page, error)
	Get(context.Context, string, uuid.UUID) (json.RawMessage, error)
	Execute(context.Context, operations.Mutation) (json.RawMessage, error)
}

type collectionUseCases interface {
	CollectSite(context.Context, uuid.UUID) (collection.Result, error)
	ListStatus(context.Context, *uuid.UUID) ([]collection.Status, error)
}

type evaluationUseCases interface {
	RequestRun(context.Context, evaluationapp.RunCommand) (evaluationapp.Run, error)
	GetRun(context.Context, uuid.UUID) (evaluationapp.Run, error)
	ListRuns(context.Context, evaluationapp.RunFilter) (evaluationapp.RunPage, error)
	PromoteReference(context.Context, evaluationapp.ReferenceCommand) (domainevaluation.ModelReference, error)
}

type scoringUseCases interface {
	Refresh(context.Context) error
	ListInsights(context.Context, scoringapp.InsightFilter) (scoringapp.InsightPage, error)
}

type HandlerOption func(*Handler)

func WithOperations(service operationsUseCases) HandlerOption {
	return func(handler *Handler) { handler.operations = service }
}

func WithCollection(service collectionUseCases) HandlerOption {
	return func(handler *Handler) { handler.collection = service }
}

func WithEvaluation(service evaluationUseCases) HandlerOption {
	return func(handler *Handler) { handler.evaluation = service }
}

func WithScoring(service scoringUseCases) HandlerOption {
	return func(handler *Handler) { handler.scoring = service }
}

type Handler struct {
	onboarding     onboardingUseCases
	reconciliation reconciliationUseCases
	idempotency    *idempotency.Service
	logger         *slog.Logger
	operations     operationsUseCases
	collection     collectionUseCases
	evaluation     evaluationUseCases
	scoring        scoringUseCases
}

func NewHandler(
	onboardingService onboardingUseCases,
	reconciliationService reconciliationUseCases,
	idempotencyService *idempotency.Service,
	logger *slog.Logger,
	options ...HandlerOption,
) (*Handler, error) {
	if onboardingService == nil || reconciliationService == nil || idempotencyService == nil || logger == nil {
		return nil, errors.New("HTTP handler dependencies are required")
	}
	handler := &Handler{
		onboarding: onboardingService, reconciliation: reconciliationService,
		idempotency: idempotencyService, logger: logger,
	}
	for _, option := range options {
		option(handler)
	}
	return handler, nil
}

var _ apispec.ServerInterface = (*Handler)(nil)

func (h *Handler) GetHealth(c *gin.Context) {
	c.JSON(stdhttp.StatusOK, apispec.HealthResponse{Status: apispec.Ok})
}
