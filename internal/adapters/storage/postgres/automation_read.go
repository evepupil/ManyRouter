package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	automationapp "github.com/evepupil/ManyRouter/internal/application/automation"
	domainautomation "github.com/evepupil/ManyRouter/internal/domain/automation"
	operationsdomain "github.com/evepupil/ManyRouter/internal/domain/operations"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) ListReadyScoreRuns(ctx context.Context, limit int) ([]automationapp.ScoreRun, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("ready score run limit is invalid")
	}
	rows, err := store.pool.Query(ctx, `
		SELECT run.id,run.site_id,run.policy_version,run.window_end,
		       run.expected_targets,run.completed_targets,run.status
		FROM score_runs run
		WHERE run.status='succeeded'
		  AND NOT EXISTS(SELECT 1 FROM automation_runs applied WHERE applied.score_run_id=run.id AND applied.status<>'preview')
		  AND EXISTS(
			SELECT 1
			FROM site_strategies strategy
			JOIN strategy_automation_settings setting ON setting.strategy_id=strategy.id
			WHERE strategy.site_id=run.site_id AND strategy.enabled AND setting.mode='automatic'
		  )
		ORDER BY run.window_end,run.site_id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]automationapp.ScoreRun, 0)
	for rows.Next() {
		var run automationapp.ScoreRun
		if err := rows.Scan(&run.ID, &run.SiteID, &run.PolicyVersion, &run.WindowEnd, &run.ExpectedTargets, &run.CompletedTargets, &run.Status); err != nil {
			return nil, err
		}
		run.WindowEnd = run.WindowEnd.UTC()
		result = append(result, run)
	}
	return result, rows.Err()
}

func (store *Store) GetLatestSuccessfulScoreRun(ctx context.Context, siteID uuid.UUID) (automationapp.ScoreRun, error) {
	var run automationapp.ScoreRun
	err := store.pool.QueryRow(ctx, `
		SELECT id,site_id,policy_version,window_end,expected_targets,completed_targets,status
		FROM score_runs
		WHERE site_id=$1 AND status='succeeded'
		ORDER BY window_end DESC,id DESC
		LIMIT 1
	`, siteID).Scan(
		&run.ID, &run.SiteID, &run.PolicyVersion, &run.WindowEnd,
		&run.ExpectedTargets, &run.CompletedTargets, &run.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return automationapp.ScoreRun{}, operationsdomain.ErrNotFound
	}
	if err != nil {
		return automationapp.ScoreRun{}, err
	}
	run.WindowEnd = run.WindowEnd.UTC()
	return run, nil
}

func (store *Store) LoadAutomationInput(ctx context.Context, scoreRunID uuid.UUID) (automationapp.Input, error) {
	var input automationapp.Input
	err := store.pool.QueryRow(ctx, `
		SELECT id,site_id,policy_version,window_end,expected_targets,completed_targets,status
		FROM score_runs WHERE id=$1
	`, scoreRunID).Scan(
		&input.ScoreRun.ID, &input.ScoreRun.SiteID, &input.ScoreRun.PolicyVersion,
		&input.ScoreRun.WindowEnd, &input.ScoreRun.ExpectedTargets,
		&input.ScoreRun.CompletedTargets, &input.ScoreRun.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return automationapp.Input{}, operationsdomain.ErrNotFound
	}
	if err != nil {
		return automationapp.Input{}, err
	}
	input.ScoreRun.WindowEnd = input.ScoreRun.WindowEnd.UTC()
	var snapshotCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM score_snapshots WHERE score_run_id=$1`, scoreRunID).Scan(&snapshotCount); err != nil {
		return automationapp.Input{}, err
	}
	if input.ScoreRun.Status == "succeeded" && snapshotCount != input.ScoreRun.ExpectedTargets {
		return automationapp.Input{}, errors.New("successful score run does not contain every target")
	}
	strategyRows, err := store.pool.Query(ctx, `
		SELECT strategy.id,strategy.kind,strategy.display_name,strategy.enabled,strategy.visible,strategy.version,
		       COALESCE(setting.mode,'manual'),COALESCE(setting.version,0),
		       COALESCE(setting.entry_closed_by_automation,false)
		FROM site_strategies strategy
		LEFT JOIN strategy_automation_settings setting ON setting.strategy_id=strategy.id
		WHERE strategy.site_id=$1
		ORDER BY strategy.kind
	`, input.ScoreRun.SiteID)
	if err != nil {
		return automationapp.Input{}, err
	}
	strategies := make([]automationapp.StrategyInput, 0, 5)
	strategyByID := make(map[uuid.UUID]int)
	strategyByKind := make(map[string]int)
	for strategyRows.Next() {
		var strategy automationapp.StrategyInput
		if err := strategyRows.Scan(
			&strategy.ID, &strategy.Kind, &strategy.DisplayName, &strategy.Enabled, &strategy.Visible,
			&strategy.Version, &strategy.Mode, &strategy.SettingVersion, &strategy.EntryClosedByAutomation,
		); err != nil {
			strategyRows.Close()
			return automationapp.Input{}, err
		}
		strategyByID[strategy.ID] = len(strategies)
		strategyByKind[strategy.Kind] = len(strategies)
		strategies = append(strategies, strategy)
	}
	if err := strategyRows.Err(); err != nil {
		strategyRows.Close()
		return automationapp.Input{}, err
	}
	strategyRows.Close()
	members := make(map[uuid.UUID]map[uuid.UUID]bool, len(strategies))
	memberRows, err := store.pool.Query(ctx, `
		SELECT member.strategy_id,member.relation_id
		FROM strategy_members member
		JOIN site_strategies strategy ON strategy.id=member.strategy_id
		WHERE strategy.site_id=$1
		ORDER BY member.strategy_id,member.relation_id
	`, input.ScoreRun.SiteID)
	if err != nil {
		return automationapp.Input{}, err
	}
	for memberRows.Next() {
		var strategyID, relationID uuid.UUID
		if err := memberRows.Scan(&strategyID, &relationID); err != nil {
			memberRows.Close()
			return automationapp.Input{}, err
		}
		if members[strategyID] == nil {
			members[strategyID] = make(map[uuid.UUID]bool)
		}
		members[strategyID][relationID] = true
	}
	if err := memberRows.Err(); err != nil {
		memberRows.Close()
		return automationapp.Input{}, err
	}
	memberRows.Close()
	for strategyID, index := range strategyByID {
		for relationID := range members[strategyID] {
			strategies[index].CurrentMemberIDs = append(strategies[index].CurrentMemberIDs, relationID)
		}
		sort.Slice(strategies[index].CurrentMemberIDs, func(i, j int) bool {
			return strategies[index].CurrentMemberIDs[i].String() < strategies[index].CurrentMemberIDs[j].String()
		})
	}
	candidateIndexes := make(map[uuid.UUID]map[uuid.UUID]int, len(strategies))
	adviceCounts := make(map[uuid.UUID]int, len(strategies))
	rows, err := store.pool.Query(ctx, `
		SELECT snapshot.id,relation.id,snapshot.supplier_id,supplier.name,snapshot.model,
		       snapshot.hard_reasons,recommendation.strategy_kind,recommendation.action,
		       recommendation.reasons,
		       EXISTS(SELECT 1 FROM site_supplier_automation_holds hold WHERE hold.relation_id=relation.id AND hold.active)
		FROM score_snapshots snapshot
		JOIN site_suppliers relation ON relation.site_id=snapshot.site_id AND relation.supplier_id=snapshot.supplier_id
		JOIN suppliers supplier ON supplier.id=snapshot.supplier_id
		JOIN shadow_recommendations recommendation ON recommendation.score_snapshot_id=snapshot.id
		WHERE snapshot.score_run_id=$1
		ORDER BY recommendation.strategy_kind,relation.id,snapshot.model
	`, scoreRunID)
	if err != nil {
		return automationapp.Input{}, err
	}
	for rows.Next() {
		var snapshotID, relationID, supplierID uuid.UUID
		var supplierName, model, strategyKind, action string
		var hardJSON, reasonJSON []byte
		var held bool
		if err := rows.Scan(
			&snapshotID, &relationID, &supplierID, &supplierName, &model,
			&hardJSON, &strategyKind, &action, &reasonJSON, &held,
		); err != nil {
			rows.Close()
			return automationapp.Input{}, err
		}
		strategyIndex, exists := strategyByKind[strategyKind]
		if !exists {
			continue
		}
		var hardReasons []domainscoring.GateReason
		if err := json.Unmarshal(hardJSON, &hardReasons); err != nil {
			rows.Close()
			return automationapp.Input{}, err
		}
		var reasons []string
		if err := json.Unmarshal(reasonJSON, &reasons); err != nil {
			rows.Close()
			return automationapp.Input{}, err
		}
		strategyID := strategies[strategyIndex].ID
		if candidateIndexes[strategyID] == nil {
			candidateIndexes[strategyID] = make(map[uuid.UUID]int)
		}
		candidateIndex, exists := candidateIndexes[strategyID][relationID]
		if !exists {
			candidateIndex = len(strategies[strategyIndex].Candidates)
			candidateIndexes[strategyID][relationID] = candidateIndex
			strategies[strategyIndex].Candidates = append(strategies[strategyIndex].Candidates, automationapp.Candidate{
				RelationID: relationID, SupplierID: supplierID, SupplierName: supplierName,
				CurrentMember: members[strategyID][relationID], Held: held,
			})
		}
		candidate := &strategies[strategyIndex].Candidates[candidateIndex]
		candidate.Models = append(candidate.Models, domainautomation.ModelAdvice{
			Model: model, SnapshotID: snapshotID, Action: domainscoring.AdviceAction(action),
			Reasons: reasons, HardReasons: hardReasons,
		})
		adviceCounts[strategyID]++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return automationapp.Input{}, err
	}
	rows.Close()
	for index := range strategies {
		strategy := &strategies[index]
		if adviceCounts[strategy.ID] != snapshotCount {
			return automationapp.Input{}, fmt.Errorf("strategy %s does not contain every score recommendation", strategy.Kind)
		}
		for candidateIndex := range strategy.Candidates {
			sort.Slice(strategy.Candidates[candidateIndex].Models, func(i, j int) bool {
				return strategy.Candidates[candidateIndex].Models[i].Model < strategy.Candidates[candidateIndex].Models[j].Model
			})
		}
		sort.Slice(strategy.Candidates, func(i, j int) bool {
			return strategy.Candidates[i].RelationID.String() < strategy.Candidates[j].RelationID.String()
		})
	}
	input.Strategies = strategies
	return input, nil
}

func (store *Store) ListAutomationSettings(ctx context.Context, siteID uuid.UUID) ([]automationapp.Setting, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT strategy.id,strategy.site_id,strategy.kind,strategy.display_name,
		       COALESCE(setting.mode,'manual'),COALESCE(setting.version,0),
		       COALESCE(setting.entry_closed_by_automation,false),
		       COALESCE(setting.reason,''),COALESCE(setting.updated_by,''),
		       COALESCE(setting.updated_at,strategy.updated_at)
		FROM site_strategies strategy
		LEFT JOIN strategy_automation_settings setting ON setting.strategy_id=strategy.id
		WHERE strategy.site_id=$1
		ORDER BY strategy.kind
	`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]automationapp.Setting, 0, 5)
	for rows.Next() {
		var setting automationapp.Setting
		if err := rows.Scan(
			&setting.StrategyID, &setting.SiteID, &setting.StrategyKind, &setting.DisplayName,
			&setting.Mode, &setting.Version, &setting.EntryClosedByAutomation,
			&setting.Reason, &setting.UpdatedBy, &setting.UpdatedAt,
		); err != nil {
			return nil, err
		}
		setting.UpdatedAt = setting.UpdatedAt.UTC()
		result = append(result, setting)
	}
	return result, rows.Err()
}

type automationQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadAutomationRun(ctx context.Context, queryer automationQueryer, runID uuid.UUID) (automationapp.Run, error) {
	var result automationapp.Run
	var routePlanID pgtype.UUID
	var routePlanStatus pgtype.Text
	err := queryer.QueryRow(ctx, `
		SELECT run.id,run.site_id,run.score_run_id,run.status,run.trigger_kind,run.route_plan_id,
		       run.summary,run.started_at,run.completed_at,plan.status
		FROM automation_runs run
		LEFT JOIN route_plan_versions plan ON plan.id=run.route_plan_id
		WHERE run.id=$1
	`, runID).Scan(
		&result.ID, &result.SiteID, &result.ScoreRunID, &result.Status, &result.TriggerKind,
		&routePlanID, &result.Summary, &result.StartedAt, &result.CompletedAt, &routePlanStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return automationapp.Run{}, operationsdomain.ErrNotFound
	}
	if err != nil {
		return automationapp.Run{}, err
	}
	if routePlanID.Valid {
		id := uuid.UUID(routePlanID.Bytes)
		result.RoutePlanID = &id
		switch routePlanStatus.String {
		case "confirmed":
			result.Status = automationapp.RunSucceeded
		case "failed", "superseded":
			result.Status = automationapp.RunFailed
		case "uncertain":
			result.Status = automationapp.RunUncertain
		}
	}
	result.StartedAt = result.StartedAt.UTC()
	result.CompletedAt = result.CompletedAt.UTC()
	rows, err := queryer.Query(ctx, `
		SELECT decision.id,strategy.kind,decision.relation_id,supplier.name,decision.action,
		       decision.current_member,decision.target_member,decision.hold_action,
		       decision.reasons,decision.created_at
		FROM automation_decisions decision
		JOIN site_strategies strategy ON strategy.id=decision.strategy_id
		JOIN site_suppliers relation ON relation.id=decision.relation_id
		JOIN suppliers supplier ON supplier.id=relation.supplier_id
		WHERE decision.run_id=$1
		ORDER BY strategy.kind,supplier.name,decision.relation_id
	`, runID)
	if err != nil {
		return automationapp.Run{}, err
	}
	defer rows.Close()
	result.Decisions = make([]automationapp.DecisionView, 0)
	for rows.Next() {
		var decision automationapp.DecisionView
		var reasons []byte
		if err := rows.Scan(
			&decision.ID, &decision.StrategyKind, &decision.RelationID, &decision.SupplierName,
			&decision.Action, &decision.CurrentMember, &decision.TargetMember,
			&decision.HoldAction, &reasons, &decision.CreatedAt,
		); err != nil {
			return automationapp.Run{}, err
		}
		if err := json.Unmarshal(reasons, &decision.Reasons); err != nil {
			return automationapp.Run{}, err
		}
		decision.CreatedAt = decision.CreatedAt.UTC()
		result.Decisions = append(result.Decisions, decision)
	}
	return result, rows.Err()
}

func (store *Store) ListAutomationRuns(ctx context.Context, filter automationapp.RunFilter) (automationapp.RunPage, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id,count(*) OVER()::bigint
		FROM automation_runs
		WHERE ($1::uuid IS NULL OR site_id=$1)
		ORDER BY started_at DESC,id DESC
		LIMIT $2 OFFSET $3
	`, databaseUUIDPointer(filter.SiteID), filter.Limit, filter.Offset)
	if err != nil {
		return automationapp.RunPage{}, err
	}
	type entry struct {
		id    uuid.UUID
		total int64
	}
	entries := make([]entry, 0)
	for rows.Next() {
		var item entry
		if err := rows.Scan(&item.id, &item.total); err != nil {
			rows.Close()
			return automationapp.RunPage{}, err
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return automationapp.RunPage{}, err
	}
	rows.Close()
	page := automationapp.RunPage{Items: make([]automationapp.Run, 0, len(entries)), Limit: filter.Limit, Offset: filter.Offset}
	for _, entry := range entries {
		run, err := loadAutomationRun(ctx, store.pool, entry.id)
		if err != nil {
			return automationapp.RunPage{}, err
		}
		page.Items = append(page.Items, run)
		page.Total = entry.total
	}
	return page, nil
}

var _ automationapp.Repository = (*Store)(nil)
