package httptransport

import (
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/transport/http/apispec"
)

func siteResponse(data site.Site) apispec.SiteResponse {
	return apispec.SiteResponse{
		Id:                  data.ID,
		Code:                data.Code,
		Name:                data.Name,
		NewApiBaseUrl:       data.NewAPIBaseURL,
		Status:              apispec.SiteResponseStatus(data.Status),
		CompatibilityStatus: apispec.SiteResponseCompatibilityStatus(data.CompatibilityStatus),
		Version:             data.Version,
		CreatedAt:           data.CreatedAt,
		UpdatedAt:           data.UpdatedAt,
	}
}

func supplierResponse(data supplier.Supplier) apispec.SupplierResponse {
	models := make([]apispec.SupplierModelResponse, 0, len(data.Models))
	for _, model := range data.Models {
		models = append(models, apispec.SupplierModelResponse{
			Model:         model.Name,
			UpstreamModel: model.UpstreamName,
			InputPrice:    model.InputPrice.String(),
			OutputPrice:   model.OutputPrice.String(),
			Currency:      model.Currency,
			Enabled:       model.Enabled,
		})
	}
	return apispec.SupplierResponse{
		Id:              data.ID,
		Code:            data.Code,
		Name:            data.Name,
		Protocol:        apispec.SupplierResponseProtocol(data.Protocol),
		UpstreamBaseUrl: data.UpstreamBaseURL,
		Status:          apispec.SupplierResponseStatus(data.Status),
		Version:         data.Version,
		Models:          models,
		CreatedAt:       data.CreatedAt,
		UpdatedAt:       data.UpdatedAt,
	}
}

func siteSupplierResponse(data routing.Relation) apispec.SiteSupplierResponse {
	return apispec.SiteSupplierResponse{
		Id:               data.ID,
		SiteId:           data.SiteID,
		SupplierId:       data.SupplierID,
		GroupKey:         data.GroupKey,
		GroupDisplayName: data.GroupDisplayName,
		SaleRatio:        data.SaleRatio.String(),
		Visible:          data.Visible,
		DesiredStatus:    apispec.SiteSupplierResponseDesiredStatus(data.DesiredStatus),
		SyncStatus:       apispec.SiteSupplierResponseSyncStatus(data.SyncStatus),
		RoutePlanVersion: data.CurrentPlanVersion,
		LastConfirmedAt:  data.LastConfirmedAt,
		CreatedAt:        data.CreatedAt,
		UpdatedAt:        data.UpdatedAt,
	}
}

func syncOperationResponse(data reconciliation.Operation) apispec.SyncOperationResponse {
	return apispec.SyncOperationResponse{
		Id:               data.ID,
		RelationId:       data.RelationID,
		RoutePlanId:      data.RoutePlanID,
		Status:           apispec.SyncOperationResponseStatus(data.Status),
		CurrentStep:      data.CurrentStep,
		Attempt:          data.Attempt,
		LastErrorCode:    data.LastErrorCode,
		LastErrorMessage: data.LastErrorMessage,
		CreatedAt:        data.CreatedAt,
		UpdatedAt:        data.UpdatedAt,
		CompletedAt:      data.CompletedAt,
	}
}
