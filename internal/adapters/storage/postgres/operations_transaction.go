package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type operationTx struct {
	tx       pgx.Tx
	mutation domain.Mutation
	now      time.Time
	affected map[uuid.UUID]bool
	resumes  map[uuid.UUID]bool
}

func (s *Store) MutateOperations(ctx context.Context, m domain.Mutation) (json.RawMessage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scope := "ops:" + m.Actor + ":" + m.Kind + ":" + m.ID.String() + ":" + m.StrategyKind
	locked, err := tryTransactionLock(ctx, tx, scope+":"+m.Key, 0)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, domain.ErrBusy
	}
	var oldHash string
	var oldResponse []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_body FROM idempotency_records WHERE scope=$1 AND idempotency_key=$2 AND expires_at>now()`, scope, m.Key).Scan(&oldHash, &oldResponse)
	if err == nil {
		if oldHash != m.RequestHash {
			return nil, fmt.Errorf("%w: 相同请求编号已用于其他内容", domain.ErrConflict)
		}
		return oldResponse, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// A short configuration transaction serializes operator edits; network synchronization uses independent site locks.
	locked, err = tryTransactionLock(ctx, tx, "manyrouter_operator_configuration", 2)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, domain.ErrBusy
	}
	operation := &operationTx{tx: tx, mutation: m, now: time.Now().UTC(), affected: make(map[uuid.UUID]bool), resumes: make(map[uuid.UUID]bool)}
	if err = operation.lockTargets(ctx); err != nil {
		return nil, err
	}
	result, err := operation.apply(ctx)
	if err != nil {
		return nil, err
	}
	siteIDs := sortedSites(operation.affected)
	plans := make([]json.RawMessage, 0, len(siteIDs))
	for _, id := range siteIDs {
		plan, err := operation.buildSitePlan(ctx, id)
		if err != nil {
			return nil, err
		}
		if plan != nil {
			plans = append(plans, plan)
		}
	}
	if result == nil {
		result = map[string]any{"plans": plans}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency_records(scope,idempotency_key,request_hash,status_code,response_body,created_at,expires_at)
        VALUES($1,$2,$3,200,$4,$5,$6) ON CONFLICT(scope,idempotency_key) DO UPDATE SET request_hash=EXCLUDED.request_hash,response_body=EXCLUDED.response_body,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at`, scope, m.Key, m.RequestHash, raw, operation.now, operation.now.Add(24*time.Hour)); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return raw, nil
}

func (o *operationTx) lockTargets(ctx context.Context) error {
	var query string
	var arg any
	switch o.mutation.Kind {
	case "update_site", "strategy", "sync":
		o.affected[o.mutation.ID] = true
	case "update_supplier", "rotate_credential", "cancel_credential":
		query = `SELECT site_id FROM site_suppliers WHERE supplier_id=$1`
		arg = o.mutation.ID
	case "relation":
		query = `SELECT site_id FROM site_suppliers WHERE id=$1`
		arg = o.mutation.ID
	case "publish_price":
		query = `SELECT site_id FROM price_versions WHERE id=$1`
		arg = o.mutation.ID
	case "restore":
		query = `SELECT site_id FROM route_plan_versions WHERE id=$1`
		arg = o.mutation.ID
	case "deploy":
		for _, target := range o.mutation.Input.(domain.DeploymentInput).Sites {
			o.affected[target.SiteID] = true
		}
	case "draft_price":
		o.affected[o.mutation.Input.(domain.PriceInput).SiteID] = true
	}
	if query != "" {
		rows, err := o.tx.Query(ctx, query, arg)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			o.affected[id] = true
		}
	}
	for _, id := range sortedSites(o.affected) {
		locked, err := tryTransactionLock(ctx, o.tx, id.String(), 1)
		if err != nil {
			return err
		}
		if !locked {
			return domain.ErrBusy
		}
	}
	return nil
}

func tryTransactionLock(ctx context.Context, tx pgx.Tx, key string, seed int64) (bool, error) {
	var locked bool
	err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1::text,$2))`, key, seed).Scan(&locked)
	return locked, err
}

func sortedSites(ids map[uuid.UUID]bool) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func (o *operationTx) audit(ctx context.Context, siteID uuid.UUID, objectType string, id uuid.UUID, reason string) error {
	if reason == "" {
		reason = "运营录入"
	}
	_, err := o.tx.Exec(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,site_id,object_type,object_id,action,reason,result,created_at)
        VALUES($1,'operator',$2,$3,$4,$5,$6,$7,'succeeded',$8)`, uuid.New(), o.mutation.Actor, databaseUUID(siteID), objectType, id.String(), o.mutation.Kind, reason, o.now)
	return err
}

func (o *operationTx) apply(ctx context.Context) (any, error) {
	switch o.mutation.Kind {
	case "create_site", "update_site":
		return o.saveSite(ctx, o.mutation.Input.(domain.SiteInput))
	case "create_supplier", "update_supplier":
		return o.saveSupplier(ctx, o.mutation.Input.(domain.SupplierInput))
	case "rotate_credential":
		return nil, o.rotateCredential(ctx, o.mutation.Input.(domain.CredentialInput))
	case "cancel_credential":
		return nil, o.cancelCredential(ctx, o.mutation.Input.(domain.CredentialCancelInput))
	case "deploy":
		return nil, o.deploy(ctx, o.mutation.Input.(domain.DeploymentInput))
	case "relation":
		return nil, o.saveRelation(ctx, o.mutation.Input.(domain.RelationInput))
	case "strategy":
		return o.saveStrategy(ctx, o.mutation.Input.(domain.StrategyInput))
	case "draft_price":
		return o.draftPrice(ctx, o.mutation.Input.(domain.PriceInput))
	case "publish_price":
		return nil, o.publishPrice(ctx, o.mutation.Input.(domain.PublishInput))
	case "restore":
		return nil, o.restore(ctx, o.mutation.Input.(domain.RestoreInput))
	case "sync":
		return nil, nil
	default:
		return nil, domain.ErrInvalid
	}
}

func requireUpdated(count int64, err error) error {
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) GetOperationReplay(ctx context.Context, m domain.Mutation) (json.RawMessage, bool, error) {
	scope := "ops:" + m.Actor + ":" + m.Kind + ":" + m.ID.String() + ":" + m.StrategyKind
	var hash string
	var response []byte
	err := s.pool.QueryRow(ctx, `SELECT request_hash,response_body FROM idempotency_records WHERE scope=$1 AND idempotency_key=$2 AND expires_at>now()`, scope, m.Key).Scan(&hash, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if hash != m.RequestHash {
		return nil, true, domain.ErrConflict
	}
	return response, true, nil
}
