package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (o *operationTx) restore(ctx context.Context, input domain.RestoreInput) error {
	var payload []byte
	var siteID uuid.UUID
	if err := o.tx.QueryRow(ctx, `SELECT site_id,snapshot FROM route_plan_versions WHERE id=$1`, o.mutation.ID).Scan(&siteID, &payload); err != nil {
		return err
	}
	snapshot, err := routing.DecodeSnapshot(payload)
	if err != nil {
		return err
	}
	resources := snapshot.Resources
	if snapshot.SchemaVersion == routing.SnapshotSchemaVersion {
		resources = []routing.Snapshot{snapshot}
	} else {
		if _, err = o.tx.Exec(ctx, `UPDATE site_suppliers SET desired_status='disabled',version=version+1,updated_at=$2 WHERE site_id=$1`, siteID, o.now); err != nil {
			return err
		}
	}
	for _, resource := range resources {
		for _, model := range resource.Channel.Models {
			var available bool
			if err = o.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supplier_models WHERE supplier_id=$1 AND model=$2 AND upstream_model=$3 AND enabled)`, resource.SupplierID, model.Model, model.UpstreamModel).Scan(&available); err != nil {
				return err
			}
			if !available {
				return fmt.Errorf("%w: 历史线路中的模型或映射已不再可用，无法恢复", domain.ErrConflict)
			}
		}
		tag, err := o.tx.Exec(ctx, `UPDATE site_suppliers SET group_display_name=$3,visible=$4,desired_status=$5,version=version+1,updated_at=$6 WHERE id=$1 AND site_id=$2`, resource.RelationID, siteID, resource.Group.DisplayName, resource.Group.Visible, string(resource.Channel.DesiredStatus), o.now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: 历史线路中的投放关系已不存在", domain.ErrConflict)
		}
	}
	// Restoring a route keeps current shared supplier credentials and independently published prices.
	if snapshot.SchemaVersion == routing.SiteSnapshotSchemaVersion && len(snapshot.StrategyVersions) > 0 {
		rows, err := o.tx.Query(ctx, `SELECT id,group_key,version,display_name FROM site_strategies WHERE site_id=$1`, siteID)
		if err != nil {
			return err
		}
		type current struct {
			id      uuid.UUID
			key     string
			version int64
			name    string
		}
		strategies, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (current, error) {
			var v current
			err := row.Scan(&v.id, &v.key, &v.version, &v.name)
			return v, err
		})
		if err != nil {
			return err
		}
		for _, strategy := range strategies {
			var sourceVersion int64
			for _, reference := range snapshot.StrategyVersions {
				if reference.ID == strategy.id {
					sourceVersion = reference.Version
				}
			}
			if sourceVersion == 0 {
				continue
			}
			var source []byte
			if err = o.tx.QueryRow(ctx, `SELECT snapshot FROM strategy_versions WHERE strategy_id=$1 AND version=$2`, strategy.id, sourceVersion).Scan(&source); err != nil {
				return err
			}
			var state domain.StrategyInput
			if err = json.Unmarshal(source, &state); err != nil {
				return err
			}
			state.Version = strategy.version + 1
			state.Reason = input.Reason
			if _, err = o.tx.Exec(ctx, `UPDATE site_strategies SET display_name=$2,visible=$3,enabled=$4,version=$5,updated_at=$6 WHERE id=$1`, strategy.id, state.DisplayName, state.Visible, state.Enabled, state.Version, o.now); err != nil {
				return err
			}
			if _, err = o.tx.Exec(ctx, `DELETE FROM strategy_members WHERE strategy_id=$1`, strategy.id); err != nil {
				return err
			}
			for _, id := range state.MemberRelationIDs {
				if _, err = o.tx.Exec(ctx, `INSERT INTO strategy_members(strategy_id,relation_id) VALUES($1,$2)`, strategy.id, id); err != nil {
					return err
				}
			}
			data, err := json.Marshal(state)
			if err != nil {
				return err
			}
			if _, err = o.tx.Exec(ctx, `INSERT INTO strategy_versions(strategy_id,version,snapshot,reason,actor_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, strategy.id, state.Version, data, input.Reason, o.mutation.Actor, o.now); err != nil {
				return err
			}
		}
	}
	return o.audit(ctx, siteID, "route_plan", o.mutation.ID, input.Reason)
}
