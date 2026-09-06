package postgres

import (
	"context"
	"encoding/json"
	"errors"

	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	"github.com/jackc/pgx/v5"
)

func (store *Store) SaveProductSnapshot(ctx context.Context, snapshot domaincatalog.Snapshot, contentHash string) (domaincatalog.Snapshot, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,3))`, snapshot.SiteID.String()); err != nil {
		return domaincatalog.Snapshot{}, err
	}
	var previousVersion int64
	var previousHash string
	var previousContent []byte
	err = tx.QueryRow(ctx, `
		SELECT version,content_hash,content
		FROM site_product_snapshots
		WHERE site_id=$1
		ORDER BY version DESC LIMIT 1
	`, snapshot.SiteID).Scan(&previousVersion, &previousHash, &previousContent)
	if err == nil && previousHash == contentHash {
		var previous domaincatalog.Snapshot
		if err := json.Unmarshal(previousContent, &previous); err != nil {
			return domaincatalog.Snapshot{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domaincatalog.Snapshot{}, err
		}
		return previous, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domaincatalog.Snapshot{}, err
	}
	snapshot.Version = previousVersion + 1
	snapshot.ContentHash = contentHash
	content, err := json.Marshal(snapshot)
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO site_product_snapshots(
			id,site_id,version,route_plan_id,score_run_id,content,content_hash,facts_through,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, snapshot.ID, snapshot.SiteID, snapshot.Version, snapshot.RoutePlanID,
		databaseUUIDPointer(snapshot.ScoreRunID), content, contentHash,
		optionalDatabaseTime(snapshot.FactsThrough), snapshot.GeneratedAt.UTC())
	if err != nil {
		return domaincatalog.Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domaincatalog.Snapshot{}, err
	}
	return snapshot, nil
}

var _ interface {
	SaveProductSnapshot(context.Context, domaincatalog.Snapshot, string) (domaincatalog.Snapshot, error)
} = (*Store)(nil)
