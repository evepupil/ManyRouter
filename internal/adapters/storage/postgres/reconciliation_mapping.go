package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/jackc/pgx/v5/pgtype"
)

func mapOperation(row sqlc.SyncOperation) (reconciliation.Operation, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return reconciliation.Operation{}, errors.New("synchronization operation timestamps are invalid")
	}
	return reconciliation.Operation{
		ID: row.ID, SiteID: row.SiteID, RelationID: row.SiteSupplierID, RoutePlanID: row.RoutePlanID,
		Status: reconciliation.OperationStatus(row.Status), CurrentStep: optionalString(row.CurrentStep),
		Attempt: int(row.Attempt), LastErrorCode: optionalString(row.LastErrorCode),
		LastErrorMessage: optionalString(row.LastErrorMessage), NextAttemptAt: optionalTime(row.NextAttemptAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), CompletedAt: optionalTime(row.CompletedAt),
	}, nil
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func optionalString(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	copyOfValue := value.String
	return &copyOfValue
}

func optionalDatabaseTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return databaseTime(*value)
}

func marshalJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal synchronization summary: %w", err)
	}
	return payload, nil
}
