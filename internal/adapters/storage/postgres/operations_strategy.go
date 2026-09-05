package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (o *operationTx) saveStrategy(ctx context.Context, input domain.StrategyInput) (any, error) {
	siteID := o.mutation.ID
	kind := o.mutation.StrategyKind
	var id uuid.UUID
	var version int64
	err := o.tx.QueryRow(ctx, `SELECT id,version FROM site_strategies WHERE site_id=$1 AND kind=$2`, siteID, kind).Scan(&id, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if version != input.Version {
		return nil, domain.ErrConflict
	}
	groupKey := domain.AutoGroupKey(siteID, kind)
	for _, relationID := range input.MemberRelationIDs {
		var eligible bool
		if err = o.tx.QueryRow(ctx, `SELECT r.desired_status='enabled' AND r.sync_status='active' AND s.status='enabled'
            FROM site_suppliers r JOIN suppliers s ON s.id=r.supplier_id WHERE r.id=$1 AND r.site_id=$2`, relationID, siteID).Scan(&eligible); err != nil {
			return nil, fmt.Errorf("%w: 所选供应商未投放到该站点", domain.ErrInvalid)
		}
		if input.Enabled && !eligible {
			return nil, fmt.Errorf("%w: Auto 仅能启用已经同步成功且可用的供应商", domain.ErrInvalid)
		}
	}
	if input.Enabled {
		var hasPrice bool
		if err = o.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_versions WHERE site_id=$1 AND group_key=$2 AND status IN ('published','applied'))`, siteID, groupKey).Scan(&hasPrice); err != nil {
			return nil, err
		}
		if !hasPrice {
			return nil, fmt.Errorf("%w: 请先保存停用策略并发布售价，再启用 Auto", domain.ErrInvalid)
		}
	}
	if version == 0 {
		id = uuid.New()
		_, err = o.tx.Exec(ctx, `INSERT INTO site_strategies(id,site_id,kind,group_key,display_name,enabled,visible,version,created_at,updated_at)
            VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$8)`, id, siteID, kind, groupKey, input.DisplayName, input.Enabled, input.Visible, o.now)
	} else {
		_, err = o.tx.Exec(ctx, `UPDATE site_strategies SET display_name=$2,enabled=$3,visible=$4,version=version+1,updated_at=$5 WHERE id=$1`, id, input.DisplayName, input.Enabled, input.Visible, o.now)
	}
	if err != nil {
		return nil, err
	}
	if _, err = o.tx.Exec(ctx, `DELETE FROM strategy_members WHERE strategy_id=$1`, id); err != nil {
		return nil, err
	}
	for _, relationID := range input.MemberRelationIDs {
		if _, err = o.tx.Exec(ctx, `INSERT INTO strategy_members(strategy_id,relation_id) VALUES($1,$2)`, id, relationID); err != nil {
			return nil, err
		}
	}
	snapshot, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if _, err = o.tx.Exec(ctx, `INSERT INTO strategy_versions(strategy_id,version,snapshot,reason,actor_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, version+1, snapshot, input.Reason, o.mutation.Actor, o.now); err != nil {
		return nil, err
	}
	if err = o.audit(ctx, siteID, "strategy", id, input.Reason); err != nil {
		return nil, err
	}
	return o.readResource(ctx, "strategies", id)
}
