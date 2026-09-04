package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type auditInput struct {
	ActorType   string
	ActorID     string
	SiteID      *uuid.UUID
	ObjectType  string
	ObjectID    string
	Action      string
	Reason      string
	OperationID *uuid.UUID
	OldSummary  any
	NewSummary  any
	Result      string
	CreatedAt   time.Time
}

func insertAudit(ctx context.Context, queries *sqlc.Queries, input auditInput) error {
	oldSummary, err := marshalSummary(input.OldSummary)
	if err != nil {
		return err
	}
	newSummary, err := marshalSummary(input.NewSummary)
	if err != nil {
		return err
	}
	siteID := pgtype.UUID{}
	if input.SiteID != nil {
		siteID = databaseUUID(*input.SiteID)
	}
	operationID := pgtype.UUID{}
	if input.OperationID != nil {
		operationID = databaseUUID(*input.OperationID)
	}
	actorType := input.ActorType
	if actorType == "" {
		actorType = "operator"
	}
	return queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		ID: uuid.New(), ActorType: actorType, ActorID: input.ActorID, SiteID: siteID,
		ObjectType: input.ObjectType, ObjectID: input.ObjectID, Action: input.Action, Reason: input.Reason,
		OperationID: operationID, OldSummary: oldSummary, NewSummary: newSummary,
		Result: input.Result, CreatedAt: databaseTime(input.CreatedAt),
	})
}

func marshalSummary(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal audit summary: %w", err)
	}
	return payload, nil
}
