package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	domainautomation "github.com/evepupil/ManyRouter/internal/domain/automation"
	operationsdomain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (store *Store) RecordAutomationRun(ctx context.Context, command automationapp.ApplyCommand) (automationapp.Run, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return automationapp.Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if command.Status != automationapp.RunPreview {
		existingID, found, err := findAutomationRun(ctx, tx, command.SiteID, command.ScoreRunID)
		if err != nil {
			return automationapp.Run{}, err
		}
		if found {
			if err := tx.Commit(ctx); err != nil {
				return automationapp.Run{}, err
			}
			return loadAutomationRun(ctx, store.pool, existingID)
		}
	}
	if err := insertAutomationRun(ctx, tx, command); err != nil {
		return automationapp.Run{}, err
	}
	if err := insertAutomationDecisions(ctx, tx, command.RunID, command.Decisions); err != nil {
		return automationapp.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return automationapp.Run{}, err
	}
	return loadAutomationRun(ctx, store.pool, command.RunID)
}

func (store *Store) ApplyAutomationRun(ctx context.Context, command automationapp.ApplyCommand) (automationapp.Run, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return automationapp.Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := tryTransactionLock(ctx, tx, "manyrouter_operator_configuration", 2)
	if err != nil {
		return automationapp.Run{}, err
	}
	if !locked {
		return automationapp.Run{}, operationsdomain.ErrBusy
	}
	locked, err = tryTransactionLock(ctx, tx, command.SiteID.String(), 1)
	if err != nil {
		return automationapp.Run{}, err
	}
	if !locked {
		return automationapp.Run{}, operationsdomain.ErrBusy
	}
	existingID, found, err := findAutomationRun(ctx, tx, command.SiteID, command.ScoreRunID)
	if err != nil {
		return automationapp.Run{}, err
	}
	if found {
		if err := tx.Commit(ctx); err != nil {
			return automationapp.Run{}, err
		}
		return loadAutomationRun(ctx, store.pool, existingID)
	}
	var scoreSiteID uuid.UUID
	var scoreStatus string
	if err := tx.QueryRow(ctx, `SELECT site_id,status FROM score_runs WHERE id=$1 FOR UPDATE`, command.ScoreRunID).Scan(&scoreSiteID, &scoreStatus); err != nil {
		return automationapp.Run{}, err
	}
	if scoreSiteID != command.SiteID || scoreStatus != "succeeded" {
		return automationapp.Run{}, operationsdomain.ErrConflict
	}
	var currentPlanStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM route_plan_versions
		WHERE site_id=$1 ORDER BY version DESC LIMIT 1
	`, command.SiteID).Scan(&currentPlanStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return automationapp.Run{}, err
	}
	urgent := false
	for _, decision := range command.Decisions {
		urgent = urgent || decision.HoldAction == domainautomation.HoldApply
	}
	if !urgent && (currentPlanStatus == "pending" || currentPlanStatus == "applying" || currentPlanStatus == "uncertain") {
		return automationapp.Run{}, operationsdomain.ErrBusy
	}
	if err := insertAutomationRun(ctx, tx, command); err != nil {
		return automationapp.Run{}, err
	}
	if err := insertAutomationDecisions(ctx, tx, command.RunID, command.Decisions); err != nil {
		return automationapp.Run{}, err
	}
	if err := applyAutomationHolds(ctx, tx, command); err != nil {
		return automationapp.Run{}, err
	}
	operation := &operationTx{
		tx: tx, mutation: operationsdomain.Mutation{Kind: "automatic_strategy", Actor: "system:auto"},
		now: command.CompletedAt.UTC(), affected: map[uuid.UUID]bool{command.SiteID: true},
		resumes: make(map[uuid.UUID]bool), planReason: command.Summary,
	}
	for _, update := range command.Strategies {
		if err := applyAutomationStrategy(ctx, operation, command, update); err != nil {
			return automationapp.Run{}, err
		}
	}
	planJSON, err := operation.buildSitePlan(ctx, command.SiteID)
	if err != nil {
		return automationapp.Run{}, err
	}
	status := automationapp.RunNoChange
	summary := "自动决定已记录，线路内容没有变化"
	var routePlanID *uuid.UUID
	if len(planJSON) > 0 {
		var plan struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(planJSON, &plan); err != nil || plan.ID == uuid.Nil {
			return automationapp.Run{}, errors.New("generated automation route plan is invalid")
		}
		status = automationapp.RunPendingSync
		summary = command.Summary
		routePlanID = &plan.ID
	}
	updated, err := tx.Exec(ctx, `
		UPDATE automation_runs
		SET status=$2,route_plan_id=$3,summary=$4
		WHERE id=$1
	`, command.RunID, string(status), databaseUUIDPointer(routePlanID), summary)
	if err != nil {
		return automationapp.Run{}, err
	}
	if updated.RowsAffected() != 1 {
		return automationapp.Run{}, errors.New("automation run could not be linked to its route plan")
	}
	if err := tx.Commit(ctx); err != nil {
		return automationapp.Run{}, err
	}
	return loadAutomationRun(ctx, store.pool, command.RunID)
}

func applyAutomationStrategy(
	ctx context.Context,
	operation *operationTx,
	command automationapp.ApplyCommand,
	update automationapp.StrategyUpdate,
) error {
	var siteID uuid.UUID
	var displayName string
	var enabled bool
	var version int64
	err := operation.tx.QueryRow(ctx, `
		SELECT site_id,display_name,enabled,version
		FROM site_strategies WHERE id=$1 FOR UPDATE
	`, update.StrategyID).Scan(&siteID, &displayName, &enabled, &version)
	if err != nil {
		return err
	}
	var mode string
	var settingVersion int64
	if err := operation.tx.QueryRow(ctx, `
		SELECT mode,version FROM strategy_automation_settings WHERE strategy_id=$1 FOR UPDATE
	`, update.StrategyID).Scan(&mode, &settingVersion); err != nil {
		return err
	}
	if siteID != command.SiteID || !enabled || mode != string(automationapp.ModeAutomatic) ||
		version != update.ExpectedStrategyVersion || settingVersion != update.ExpectedSettingVersion {
		return operationsdomain.ErrConflict
	}
	for _, relationID := range update.MemberRelationIDs {
		var valid bool
		if err := operation.tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM site_suppliers relation
				JOIN suppliers supplier ON supplier.id=relation.supplier_id
				WHERE relation.id=$1 AND relation.site_id=$2
				  AND relation.desired_status='enabled' AND relation.sync_status='active'
				  AND supplier.status='enabled'
			)
		`, relationID, command.SiteID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return operationsdomain.ErrConflict
		}
	}
	if _, err := operation.tx.Exec(ctx, `DELETE FROM strategy_members WHERE strategy_id=$1`, update.StrategyID); err != nil {
		return err
	}
	for _, relationID := range update.MemberRelationIDs {
		if _, err := operation.tx.Exec(ctx, `INSERT INTO strategy_members(strategy_id,relation_id) VALUES($1,$2)`, update.StrategyID, relationID); err != nil {
			return err
		}
	}
	result, err := operation.tx.Exec(ctx, `
		UPDATE site_strategies SET visible=$2,version=version+1,updated_at=$3
		WHERE id=$1 AND version=$4
	`, update.StrategyID, update.Visible, operation.now, version)
	if err != nil || result.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return operationsdomain.ErrConflict
	}
	result, err = operation.tx.Exec(ctx, `
		UPDATE strategy_automation_settings
		SET entry_closed_by_automation=$2
		WHERE strategy_id=$1 AND version=$3
	`, update.StrategyID, update.EntryClosedByAutomation, settingVersion)
	if err != nil || result.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return operationsdomain.ErrConflict
	}
	snapshot, err := json.Marshal(operationsdomain.StrategyInput{
		Version: version, Enabled: enabled, Visible: update.Visible, DisplayName: displayName,
		MemberRelationIDs: update.MemberRelationIDs, Reason: command.Summary,
	})
	if err != nil {
		return err
	}
	if _, err := operation.tx.Exec(ctx, `
		INSERT INTO strategy_versions(strategy_id,version,snapshot,reason,actor_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6)
	`, update.StrategyID, version+1, snapshot, command.Summary, "system:auto", operation.now); err != nil {
		return err
	}
	return operation.audit(ctx, command.SiteID, "strategy", update.StrategyID, command.Summary)
}

func applyAutomationHolds(ctx context.Context, tx pgx.Tx, command automationapp.ApplyCommand) error {
	type holdChange struct {
		apply   bool
		clear   bool
		reasons map[string]bool
	}
	changes := make(map[uuid.UUID]*holdChange)
	for _, decision := range command.Decisions {
		if decision.HoldAction == domainautomation.HoldNone {
			continue
		}
		change := changes[decision.RelationID]
		if change == nil {
			change = &holdChange{reasons: make(map[string]bool)}
			changes[decision.RelationID] = change
		}
		if decision.HoldAction == domainautomation.HoldApply {
			change.apply = true
		}
		if decision.HoldAction == domainautomation.HoldClear {
			change.clear = true
		}
		for _, reason := range decision.Reasons {
			change.reasons[reason] = true
		}
	}
	for relationID, change := range changes {
		var belongs bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM site_suppliers WHERE id=$1 AND site_id=$2)`, relationID, command.SiteID).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return operationsdomain.ErrConflict
		}
		reasons := make([]string, 0, len(change.reasons))
		for reason := range change.reasons {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		encoded, err := json.Marshal(reasons)
		if err != nil {
			return err
		}
		if change.apply {
			if _, err := tx.Exec(ctx, `
				INSERT INTO site_supplier_automation_holds(
					relation_id,active,reason_codes,source_run_id,created_at,cleared_at
				) VALUES($1,true,$2,$3,$4,NULL)
				ON CONFLICT(relation_id) DO UPDATE SET
					active=true,reason_codes=EXCLUDED.reason_codes,source_run_id=EXCLUDED.source_run_id,
					created_at=CASE WHEN site_supplier_automation_holds.active THEN site_supplier_automation_holds.created_at ELSE EXCLUDED.created_at END,
					cleared_at=NULL
			`, relationID, encoded, command.RunID, command.CompletedAt.UTC()); err != nil {
				return err
			}
			continue
		}
		if change.clear {
			if _, err := tx.Exec(ctx, `
				UPDATE site_supplier_automation_holds
				SET active=false,source_run_id=$2,cleared_at=$3
				WHERE relation_id=$1 AND active
			`, relationID, command.RunID, command.CompletedAt.UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertAutomationRun(ctx context.Context, tx pgx.Tx, command automationapp.ApplyCommand) error {
	if command.RunID == uuid.Nil || command.SiteID == uuid.Nil || command.ScoreRunID == uuid.Nil ||
		command.StartedAt.IsZero() || command.CompletedAt.IsZero() || strings.TrimSpace(command.Summary) == "" {
		return errors.New("automation run is invalid")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO automation_runs(
			id,site_id,score_run_id,status,trigger_kind,route_plan_id,summary,started_at,completed_at
		) VALUES($1,$2,$3,$4,$5,NULL,$6,$7,$8)
	`, command.RunID, command.SiteID, command.ScoreRunID, string(command.Status), string(command.TriggerKind),
		command.Summary, command.StartedAt.UTC(), command.CompletedAt.UTC())
	return err
}

func insertAutomationDecisions(ctx context.Context, tx pgx.Tx, runID uuid.UUID, decisions []automationapp.Decision) error {
	for _, decision := range decisions {
		reasons, err := json.Marshal(decision.Reasons)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO automation_decisions(
				id,run_id,strategy_id,relation_id,action,current_member,target_member,
				hold_action,reasons,score_snapshot_ids,created_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, decision.ID, runID, decision.StrategyID, decision.RelationID, string(decision.Action),
			decision.CurrentMember, decision.TargetMember, string(decision.HoldAction), reasons,
			decision.SnapshotIDs, decision.CreatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func findAutomationRun(ctx context.Context, queryer automationQueryer, siteID, scoreRunID uuid.UUID) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := queryer.QueryRow(ctx, `SELECT id FROM automation_runs WHERE site_id=$1 AND score_run_id=$2 AND status<>'preview'`, siteID, scoreRunID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

func (store *Store) UpdateAutomationSetting(
	ctx context.Context,
	command automationapp.UpdateSettingCommand,
	updatedAt time.Time,
) (automationapp.Setting, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return automationapp.Setting{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := tryTransactionLock(ctx, tx, "manyrouter_operator_configuration", 2)
	if err != nil {
		return automationapp.Setting{}, err
	}
	if !locked {
		return automationapp.Setting{}, operationsdomain.ErrBusy
	}
	locked, err = tryTransactionLock(ctx, tx, command.SiteID.String(), 1)
	if err != nil {
		return automationapp.Setting{}, err
	}
	if !locked {
		return automationapp.Setting{}, operationsdomain.ErrBusy
	}
	var strategyID uuid.UUID
	var displayName string
	if err := tx.QueryRow(ctx, `
		SELECT id,display_name FROM site_strategies WHERE site_id=$1 AND kind=$2 FOR UPDATE
	`, command.SiteID, command.StrategyKind).Scan(&strategyID, &displayName); errors.Is(err, pgx.ErrNoRows) {
		return automationapp.Setting{}, operationsdomain.ErrNotFound
	} else if err != nil {
		return automationapp.Setting{}, err
	}
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM strategy_automation_settings WHERE strategy_id=$1 FOR UPDATE`, strategyID).Scan(&currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return automationapp.Setting{}, err
	}
	if currentVersion != command.Version {
		return automationapp.Setting{}, operationsdomain.ErrConflict
	}
	if currentVersion == 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO strategy_automation_settings(
				strategy_id,mode,version,entry_closed_by_automation,reason,updated_by,updated_at
			) VALUES($1,$2,1,false,$3,$4,$5)
		`, strategyID, string(command.Mode), command.Reason, command.Actor, updatedAt.UTC())
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE strategy_automation_settings
			SET mode=$2,version=version+1,reason=$3,updated_by=$4,updated_at=$5
			WHERE strategy_id=$1 AND version=$6
		`, strategyID, string(command.Mode), command.Reason, command.Actor, updatedAt.UTC(), currentVersion)
	}
	if err != nil {
		return automationapp.Setting{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(id,actor_type,actor_id,site_id,object_type,object_id,action,reason,result,created_at)
		VALUES($1,'operator',$2,$3,'automation_setting',$4,'update_automation_setting',$5,'succeeded',$6)
	`, uuid.New(), command.Actor, command.SiteID, strategyID.String(), command.Reason, updatedAt.UTC()); err != nil {
		return automationapp.Setting{}, err
	}
	var setting automationapp.Setting
	if err := tx.QueryRow(ctx, `
		SELECT strategy.id,strategy.site_id,strategy.kind,strategy.display_name,
		       setting.mode,setting.version,setting.entry_closed_by_automation,
		       setting.reason,setting.updated_by,setting.updated_at
		FROM site_strategies strategy
		JOIN strategy_automation_settings setting ON setting.strategy_id=strategy.id
		WHERE strategy.id=$1
	`, strategyID).Scan(
		&setting.StrategyID, &setting.SiteID, &setting.StrategyKind, &setting.DisplayName,
		&setting.Mode, &setting.Version, &setting.EntryClosedByAutomation,
		&setting.Reason, &setting.UpdatedBy, &setting.UpdatedAt,
	); err != nil {
		return automationapp.Setting{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return automationapp.Setting{}, err
	}
	setting.UpdatedAt = setting.UpdatedAt.UTC()
	return setting, nil
}

var _ automationapp.Repository = (*Store)(nil)
