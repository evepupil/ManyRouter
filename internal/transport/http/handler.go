package httptransport

import (
	"context"
	"errors"
	"log/slog"
	stdhttp "net/http"

	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
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

type Handler struct {
	onboarding     onboardingUseCases
	reconciliation reconciliationUseCases
	idempotency    *idempotency.Service
	logger         *slog.Logger
}

func NewHandler(
	onboardingService onboardingUseCases,
	reconciliationService reconciliationUseCases,
	idempotencyService *idempotency.Service,
	logger *slog.Logger,
) (*Handler, error) {
	if onboardingService == nil || reconciliationService == nil || idempotencyService == nil || logger == nil {
		return nil, errors.New("HTTP handler dependencies are required")
	}
	return &Handler{
		onboarding: onboardingService, reconciliation: reconciliationService,
		idempotency: idempotencyService, logger: logger,
	}, nil
}

var _ apispec.ServerInterface = (*Handler)(nil)

func (h *Handler) GetHealth(c *gin.Context) {
	c.JSON(stdhttp.StatusOK, apispec.HealthResponse{Status: apispec.Ok})
}
