package postgres

import (
	"context"
	"time"

	"github.com/evepupil/ManyRouter/internal/application/runtimehealth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) ReadRuntimeSystemFacts(ctx context.Context, applicationTime time.Time) (runtimehealth.SystemFacts, error) {
	facts := runtimehealth.SystemFacts{DatabaseUp: true}
	var databaseTimeValue time.Time
	if err := store.pool.QueryRow(ctx, `
		SELECT clock_timestamp(),COALESCE((
			SELECT max(version_id) FROM goose_db_version WHERE is_applied
		),0)
	`).Scan(&databaseTimeValue, &facts.MigrationVersion); err != nil {
		return runtimehealth.SystemFacts{}, err
	}
	facts.DatabaseClockSkewSecond = databaseTimeValue.UTC().Sub(applicationTime.UTC()).Seconds()
	statistics := store.pool.Stat()
	facts.Pool = runtimehealth.PoolFacts{
		Open: statistics.TotalConns(), InUse: statistics.AcquiredConns(), Idle: statistics.IdleConns(),
		Max: statistics.MaxConns(), AcquireWait: statistics.EmptyAcquireCount(),
	}
	var oldestWaiting, collectionAt, scoringAt, automationAt, compatibilityAt pgtype.Timestamptz
	if err := store.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state::text IN ('available','scheduled','pending')),
			count(*) FILTER (WHERE state::text='running'),
			count(*) FILTER (WHERE state::text='retryable'),
			count(*) FILTER (WHERE state::text='discarded' AND finalized_at>=clock_timestamp()-INTERVAL '24 hours'),
			min(scheduled_at) FILTER (WHERE state::text IN ('available','scheduled','pending','retryable')),
			max(finalized_at) FILTER (WHERE kind='collect_measurements_v1' AND state::text='completed'),
			max(finalized_at) FILTER (WHERE kind='refresh_shadow_scores_v1' AND state::text='completed'),
			max(finalized_at) FILTER (WHERE kind='apply_fixed_auto_recommendations_v1' AND state::text='completed'),
			max(finalized_at) FILTER (WHERE kind='check_site_compatibility_v1' AND state::text='completed')
		FROM river_job
	`).Scan(
		&facts.Jobs.Waiting, &facts.Jobs.Running, &facts.Jobs.Retryable, &facts.Jobs.Failed,
		&oldestWaiting, &collectionAt, &scoringAt, &automationAt, &compatibilityAt,
	); err != nil {
		return runtimehealth.SystemFacts{}, err
	}
	facts.Jobs.OldestWaitingAt = optionalTime(oldestWaiting)
	facts.Periodic.CollectionAt = optionalTime(collectionAt)
	facts.Periodic.ScoringAt = optionalTime(scoringAt)
	facts.Periodic.AutomationAt = optionalTime(automationAt)
	facts.Periodic.CompatibilityAt = optionalTime(compatibilityAt)
	return facts, nil
}

func (store *Store) ListRuntimeSiteFacts(ctx context.Context) ([]runtimehealth.SiteFacts, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,site.code,site.name,site.status,
		       count(relation.id)::integer,
		       count(relation.id) FILTER (WHERE relation.sync_status IN ('failed','manual_locked'))::integer
		FROM sites site
		LEFT JOIN site_suppliers relation ON relation.site_id=site.id
		GROUP BY site.id,site.code,site.name,site.status
		ORDER BY site.code,site.id
	`)
	if err != nil {
		return nil, err
	}
	factsByID := make(map[uuid.UUID]*runtimehealth.SiteFacts)
	ordered := make([]*runtimehealth.SiteFacts, 0)
	for rows.Next() {
		item := &runtimehealth.SiteFacts{}
		if err := rows.Scan(
			&item.SiteID, &item.SiteCode, &item.SiteName, &item.SiteStatus,
			&item.RelationCount, &item.ProblemSuppliers,
		); err != nil {
			rows.Close()
			return nil, err
		}
		factsByID[item.SiteID] = item
		ordered = append(ordered, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := store.attachCompatibilityFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	if err := store.attachRouteFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	if err := store.attachCollectionFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	if err := store.attachScoringFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	if err := store.attachAutomationFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	if err := store.attachProductFacts(ctx, factsByID); err != nil {
		return nil, err
	}
	result := make([]runtimehealth.SiteFacts, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, *item)
	}
	return result, nil
}

func (store *Store) attachCompatibilityFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	reports, err := store.ListLatestCompatibilityChecks(ctx)
	if err != nil {
		return err
	}
	for index := range reports {
		report := reports[index]
		if target := targets[report.SiteID]; target != nil {
			target.Compatibility = &report
		}
	}
	return nil
}

func (store *Store) attachRouteFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,confirmed.id,confirmed.version,confirmed.confirmed_at,
		       latest.status,latest.version,latest.created_at,
		       sync.status,sync.updated_at,sync.last_error_code,sync.last_error_message,
		       pending.total,pending.oldest
		FROM sites site
		LEFT JOIN LATERAL (
			SELECT id,version,confirmed_at FROM route_plan_versions
			WHERE site_id=site.id AND status='confirmed'
			ORDER BY version DESC LIMIT 1
		) confirmed ON true
		LEFT JOIN LATERAL (
			SELECT status,version,created_at FROM route_plan_versions
			WHERE site_id=site.id ORDER BY version DESC LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT status,updated_at,last_error_code,last_error_message FROM sync_operations
			WHERE site_id=site.id ORDER BY updated_at DESC,id DESC LIMIT 1
		) sync ON true
		LEFT JOIN LATERAL (
			SELECT count(*)::bigint AS total,min(created_at) AS oldest FROM sync_operations
			WHERE site_id=site.id AND status IN ('pending','running','retryable_failed','uncertain')
		) pending ON true
		ORDER BY site.code,site.id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID uuid.UUID
		var confirmedID pgtype.UUID
		var confirmedVersion, latestVersion pgtype.Int8
		var confirmedAt, latestCreatedAt, syncAt, oldestPendingAt pgtype.Timestamptz
		var latestStatus, syncStatus, errorCode, errorMessage pgtype.Text
		var pending int64
		if err := rows.Scan(
			&siteID, &confirmedID, &confirmedVersion, &confirmedAt,
			&latestStatus, &latestVersion, &latestCreatedAt,
			&syncStatus, &syncAt, &errorCode, &errorMessage, &pending, &oldestPendingAt,
		); err != nil {
			return err
		}
		target := targets[siteID]
		if target == nil {
			continue
		}
		target.Route.ConfirmedPlanID = optionalUUID(confirmedID)
		if confirmedVersion.Valid {
			target.Route.ConfirmedVersion = confirmedVersion.Int64
		}
		target.Route.ConfirmedAt = optionalTime(confirmedAt)
		target.Route.LatestPlanStatus = textValue(latestStatus)
		if latestVersion.Valid {
			target.Route.LatestPlanVersion = latestVersion.Int64
		}
		target.Route.LatestPlanCreatedAt = optionalTime(latestCreatedAt)
		target.Route.LastSyncStatus = textValue(syncStatus)
		target.Route.LastSyncAt = optionalTime(syncAt)
		target.Route.LastSyncErrorCode = textValue(errorCode)
		target.Route.LastSyncError = textValue(errorMessage)
		target.Route.PendingOperations = pending
		target.Route.OldestPendingAt = optionalTime(oldestPendingAt)
	}
	return rows.Err()
}

func (store *Store) attachCollectionFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,cursor.last_success_at,cursor.last_error_at,
		       COALESCE(cursor.consecutive_failures,0),COALESCE(cursor.data_gap,false),cursor.source_latest_created_at
		FROM sites site LEFT JOIN collection_cursors cursor ON cursor.site_id=site.id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID uuid.UUID
		var successAt, errorAt pgtype.Timestamptz
		var failures int
		var gap bool
		var sourceLatest pgtype.Int8
		if err := rows.Scan(&siteID, &successAt, &errorAt, &failures, &gap, &sourceLatest); err != nil {
			return err
		}
		if target := targets[siteID]; target != nil {
			target.Collection.LastSuccessAt = optionalTime(successAt)
			target.Collection.LastErrorAt = optionalTime(errorAt)
			target.Collection.ConsecutiveFailures = failures
			target.Collection.DataGap = gap
			if sourceLatest.Valid && sourceLatest.Int64 > 0 {
				value := time.Unix(sourceLatest.Int64, 0).UTC()
				target.Collection.SourceLatestAt = &value
			}
		}
	}
	return rows.Err()
}

func (store *Store) attachScoringFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,score.window_end,score.status,score.completed_at
		FROM sites site
		LEFT JOIN LATERAL (
			SELECT window_end,status,completed_at FROM score_runs
			WHERE site_id=site.id ORDER BY window_end DESC,id DESC LIMIT 1
		) score ON true
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID uuid.UUID
		var windowAt, completedAt pgtype.Timestamptz
		var status pgtype.Text
		if err := rows.Scan(&siteID, &windowAt, &status, &completedAt); err != nil {
			return err
		}
		if target := targets[siteID]; target != nil {
			target.Scoring.LastWindowAt = optionalTime(windowAt)
			target.Scoring.LastStatus = textValue(status)
			target.Scoring.CompletedAt = optionalTime(completedAt)
		}
	}
	return rows.Err()
}

func (store *Store) attachAutomationFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,
		       (SELECT count(*)::integer FROM site_strategies strategy
		        JOIN strategy_automation_settings setting ON setting.strategy_id=strategy.id
		        WHERE strategy.site_id=site.id AND strategy.enabled AND setting.mode='automatic'),
		       run.status,run.completed_at
		FROM sites site
		LEFT JOIN LATERAL (
			SELECT status,completed_at FROM automation_runs
			WHERE site_id=site.id ORDER BY completed_at DESC,id DESC LIMIT 1
		) run ON true
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID uuid.UUID
		var automatic int
		var status pgtype.Text
		var completedAt pgtype.Timestamptz
		if err := rows.Scan(&siteID, &automatic, &status, &completedAt); err != nil {
			return err
		}
		if target := targets[siteID]; target != nil {
			target.Automation.AutomaticStrategies = automatic
			target.Automation.LastStatus = textValue(status)
			target.Automation.LastCompletedAt = optionalTime(completedAt)
		}
	}
	return rows.Err()
}

func (store *Store) attachProductFacts(ctx context.Context, targets map[uuid.UUID]*runtimehealth.SiteFacts) error {
	rows, err := store.pool.Query(ctx, `
		SELECT site.id,product.version,product.created_at,product.facts_through
		FROM sites site
		LEFT JOIN LATERAL (
			SELECT version,created_at,facts_through FROM site_product_snapshots
			WHERE site_id=site.id ORDER BY version DESC LIMIT 1
		) product ON true
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID uuid.UUID
		var version pgtype.Int8
		var generatedAt, factsThrough pgtype.Timestamptz
		if err := rows.Scan(&siteID, &version, &generatedAt, &factsThrough); err != nil {
			return err
		}
		if target := targets[siteID]; target != nil {
			if version.Valid {
				target.Product.Version = version.Int64
			}
			target.Product.GeneratedAt = optionalTime(generatedAt)
			target.Product.FactsThrough = optionalTime(factsThrough)
		}
	}
	return rows.Err()
}

var _ runtimehealth.Repository = (*Store)(nil)
