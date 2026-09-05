package postgres

import (
	"context"
	"fmt"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

func (o *operationTx) deploy(ctx context.Context, input domain.DeploymentInput) error {
	var status string
	if err := o.tx.QueryRow(ctx, `SELECT status FROM suppliers WHERE id=$1`, input.SupplierID).Scan(&status); err != nil {
		return err
	}
	if status != "enabled" {
		return fmt.Errorf("%w: 供应商尚未启用", domain.ErrInvalid)
	}
	for _, target := range input.Sites {
		if err := o.tx.QueryRow(ctx, `SELECT status FROM sites WHERE id=$1`, target.SiteID).Scan(&status); err != nil {
			return err
		}
		if status != "enabled" {
			return fmt.Errorf("%w: 目标站点已暂停管理", domain.ErrInvalid)
		}
		relationID := uuid.New()
		groupKey := routing.GroupKey(relationID)
		_, err := o.tx.Exec(ctx, `INSERT INTO site_suppliers(id,site_id,supplier_id,group_key,group_display_name,sale_ratio,visible,desired_status,sync_status,version,created_at,updated_at)
            VALUES($1,$2,$3,$4,$5,$6,$7,'enabled','pending',1,$8,$8)`, relationID, target.SiteID, input.SupplierID, groupKey, target.DisplayName, target.SaleRatio, target.Visible, o.now)
		if err != nil {
			return err
		}
		if _, err = o.tx.Exec(ctx, `INSERT INTO site_supplier_channels(id,site_supplier_id,managed_tag,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, uuid.New(), relationID, routing.ManagedTag(relationID), o.now); err != nil {
			return err
		}
		basis := o.mutation.Bases[target.SiteID]
		if _, err = o.insertPrice(ctx, target.SiteID, groupKey, target.SaleRatio, input.Reason, "published", basis); err != nil {
			return err
		}
		if err = o.audit(ctx, target.SiteID, "site_supplier", relationID, input.Reason); err != nil {
			return err
		}
	}
	return nil
}

func (o *operationTx) saveRelation(ctx context.Context, input domain.RelationInput) error {
	id := o.mutation.ID
	tag, err := o.tx.Exec(ctx, `UPDATE site_suppliers SET group_display_name=$2,visible=$3,desired_status=$4,version=version+1,updated_at=$5
        WHERE id=$1 AND version=$6`, id, input.DisplayName, input.Visible, input.DesiredStatus, o.now, input.Version)
	if err = requireUpdated(tag.RowsAffected(), err); err != nil {
		return err
	}
	if input.Resume {
		o.resumes[id] = true
	}
	var siteID uuid.UUID
	if err = o.tx.QueryRow(ctx, `SELECT site_id FROM site_suppliers WHERE id=$1`, id).Scan(&siteID); err != nil {
		return err
	}
	return o.audit(ctx, siteID, "site_supplier", id, input.Reason)
}
