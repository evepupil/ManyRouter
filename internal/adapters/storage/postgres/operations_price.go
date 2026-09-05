package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

func (o *operationTx) insertPrice(ctx context.Context, siteID uuid.UUID, groupKey, ratio, reason, status string, basis domain.BillingBasis) (uuid.UUID, error) {
	id := uuid.New()
	payload, err := json.Marshal(basis.Values)
	if err != nil {
		return uuid.Nil, err
	}
	if string(payload) == "null" {
		payload = []byte("{}")
	}
	_, err = o.tx.Exec(ctx, `INSERT INTO price_versions(id,site_id,group_key,version,sale_ratio,reason,status,billing_basis,basis_hash,created_at,published_at)
        VALUES($1::uuid,$2::uuid,$3::varchar(64),(SELECT COALESCE(max(version),0)+1 FROM price_versions WHERE site_id=$2::uuid AND group_key=$3::varchar(64)),$4::numeric,$5::text,$6::text,$7::jsonb,$8::text,$9::timestamptz,CASE WHEN $6::text='published' THEN $9::timestamptz ELSE NULL END)`, id, siteID, groupKey, ratio, reason, status, payload, basis.Hash, o.now)
	return id, err
}

func (o *operationTx) draftPrice(ctx context.Context, input domain.PriceInput) (any, error) {
	var exists bool
	if err := o.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM site_suppliers WHERE site_id=$1 AND group_key=$2 UNION ALL SELECT 1 FROM site_strategies WHERE site_id=$1 AND group_key=$2)`, input.SiteID, input.GroupKey).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: 该站点没有此受管分组", domain.ErrInvalid)
	}
	id, err := o.insertPrice(ctx, input.SiteID, input.GroupKey, input.SaleRatio, input.Reason, "draft", o.mutation.Bases[input.SiteID])
	if err != nil {
		return nil, err
	}
	delete(o.affected, input.SiteID)
	if err = o.audit(ctx, input.SiteID, "price", id, input.Reason); err != nil {
		return nil, err
	}
	return o.readResource(ctx, "prices", id)
}

func (o *operationTx) publishPrice(ctx context.Context, input domain.PublishInput) error {
	var siteID uuid.UUID
	var groupKey, status, hash, reason string
	var version int64
	if err := o.tx.QueryRow(ctx, `SELECT site_id,group_key,status,basis_hash,version,reason FROM price_versions WHERE id=$1`, o.mutation.ID).Scan(&siteID, &groupKey, &status, &hash, &version, &reason); err != nil {
		return err
	}
	if status != "draft" || version != input.Version {
		return domain.ErrConflict
	}
	if hash == "" || o.mutation.Bases[siteID].Hash != hash {
		return fmt.Errorf("%w: 站点计费基准已变化，请重新创建价格草案", domain.ErrConflict)
	}
	var latest int64
	if err := o.tx.QueryRow(ctx, `SELECT max(version) FROM price_versions WHERE site_id=$1 AND group_key=$2`, siteID, groupKey).Scan(&latest); err != nil {
		return err
	}
	if latest != version {
		return domain.ErrConflict
	}
	if _, err := o.tx.Exec(ctx, `UPDATE price_versions SET status='superseded' WHERE site_id=$1 AND group_key=$2 AND status IN ('published','applied')`, siteID, groupKey); err != nil {
		return err
	}
	if _, err := o.tx.Exec(ctx, `UPDATE price_versions SET status='published',published_at=$2 WHERE id=$1`, o.mutation.ID, o.now); err != nil {
		return err
	}
	if _, err := o.tx.Exec(ctx, `UPDATE site_suppliers SET sale_ratio=(SELECT sale_ratio FROM price_versions WHERE id=$3),version=version+1,updated_at=$4 WHERE site_id=$1 AND group_key=$2`, siteID, groupKey, o.mutation.ID, o.now); err != nil {
		return err
	}
	return o.audit(ctx, siteID, "price", o.mutation.ID, reason)
}
