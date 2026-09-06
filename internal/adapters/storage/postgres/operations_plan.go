package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/evepupil/ManyRouter/internal/domain/supplier"
	"github.com/evepupil/ManyRouter/internal/jobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func (o *operationTx) buildSitePlan(ctx context.Context, siteID uuid.UUID) (json.RawMessage, error) {
	queries := sqlc.New(o.tx)
	siteRow, err := queries.GetSite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	siteData, err := mapSite(siteRow)
	if err != nil {
		return nil, err
	}
	if siteData.Status == site.StatusDisabled {
		return nil, nil
	}
	rows, err := o.tx.Query(ctx, `SELECT id FROM site_suppliers WHERE site_id=$1 ORDER BY id`, siteID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	snapshot := routing.Snapshot{SchemaVersion: routing.SiteSnapshotSchemaVersion, SiteID: siteID, Resources: make([]routing.Snapshot, 0, len(ids)), AutoGroups: make([]routing.DesiredGroup, 0)}
	for _, id := range ids {
		resource, err := o.buildResource(ctx, queries, siteData, id)
		if err != nil {
			return nil, err
		}
		snapshot.Resources = append(snapshot.Resources, resource)
		if o.resumes[id] {
			snapshot.ResumeRelationIDs = append(snapshot.ResumeRelationIDs, id)
		}
	}
	snapshot.RelationID = snapshot.Resources[0].RelationID
	snapshot.SupplierID = snapshot.Resources[0].SupplierID
	if err = o.attachPricesAndStrategies(ctx, &snapshot); err != nil {
		return nil, err
	}
	payload, hash, err := routing.EncodeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	var previousID uuid.UUID
	var previousHash string
	var previousVersion int64
	err = o.tx.QueryRow(ctx, `SELECT id,content_hash,version FROM route_plan_versions WHERE site_id=$1 ORDER BY version DESC LIMIT 1`, siteID).Scan(&previousID, &previousHash, &previousVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if previousHash == hash {
		if o.mutation.Kind == "sync" {
			if err = o.enqueuePlan(ctx, previousID, siteID, snapshot.RelationID); err != nil {
				return nil, err
			}
			return o.readResource(ctx, "plans", previousID)
		}
		return nil, nil
	}
	id := uuid.New()
	reason := o.planReason
	if reason == "" {
		reason = mutationReason(o.mutation)
	}
	if _, err = o.tx.Exec(ctx, `INSERT INTO route_plan_versions(id,site_id,site_supplier_id,version,previous_plan_id,reason,snapshot,content_hash,status,created_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9)`, id, siteID, snapshot.RelationID, previousVersion+1, databaseUUID(previousID), reason, payload, hash, o.now); err != nil {
		return nil, err
	}
	if _, err = o.tx.Exec(ctx, `UPDATE site_suppliers SET current_plan_id=$2,sync_status='pending',updated_at=$3 WHERE site_id=$1`, siteID, id, o.now); err != nil {
		return nil, err
	}
	if _, err = o.tx.Exec(ctx, `UPDATE price_versions SET route_plan_id=$2 WHERE site_id=$1 AND status='published'`, siteID, id); err != nil {
		return nil, err
	}
	if err = o.enqueuePlan(ctx, id, siteID, snapshot.RelationID); err != nil {
		return nil, err
	}
	if err = o.audit(ctx, siteID, "route_plan", id, reason); err != nil {
		return nil, err
	}
	return o.readResource(ctx, "plans", id)
}

func (o *operationTx) buildResource(ctx context.Context, q *sqlc.Queries, siteData site.Site, id uuid.UUID) (routing.Snapshot, error) {
	relationRow, err := q.GetSiteSupplierDetails(ctx, id)
	if err != nil {
		return routing.Snapshot{}, err
	}
	relation, err := mapRelation(relationRow)
	if err != nil {
		return routing.Snapshot{}, err
	}
	supplierRow, err := q.GetSupplier(ctx, relation.SupplierID)
	if err != nil {
		return routing.Snapshot{}, err
	}
	models, err := q.ListSupplierModels(ctx, relation.SupplierID)
	if err != nil {
		return routing.Snapshot{}, err
	}
	supplierData, err := mapSupplier(supplierRow, models)
	if err != nil {
		return routing.Snapshot{}, err
	}
	if supplierRow.PendingCredentialID.Valid {
		supplierData.CredentialID = uuid.UUID(supplierRow.PendingCredentialID.Bytes)
		supplierData.CredentialVersion = supplierRow.PendingCredentialVersion.Int32
	}
	var automationHeld bool
	if err = o.tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM site_supplier_automation_holds WHERE relation_id=$1 AND active
	)`, id).Scan(&automationHeld); err != nil {
		return routing.Snapshot{}, err
	}
	enabled := supplierData.Status == supplier.StatusEnabled && relation.DesiredStatus != routing.DesiredDisabled && !automationHeld
	supplierData.Status = supplier.StatusEnabled
	if enabled {
		relation.DesiredStatus = routing.DesiredEnabled
	} else {
		relation.DesiredStatus = routing.DesiredDisabled
	}
	var channelID uuid.UUID
	if err = o.tx.QueryRow(ctx, `SELECT id FROM site_supplier_channels WHERE site_supplier_id=$1 ORDER BY id LIMIT 1`, id).Scan(&channelID); err != nil {
		return routing.Snapshot{}, err
	}
	channel := routing.ManagedChannel{ID: channelID, RelationID: id, ManagedTag: routing.ManagedTag(id)}
	snapshot, err := routing.BuildSnapshot(siteData, supplierData, relation, channel)
	if err != nil {
		return routing.Snapshot{}, err
	}
	snapshot.Channel.DesiredStatus = relation.DesiredStatus
	if !enabled {
		snapshot.Group.Visible = false
	}
	return snapshot, nil
}

type planPrice struct {
	ID       uuid.UUID
	GroupKey string
	Ratio    string
	Hash     string
}

func (o *operationTx) attachPricesAndStrategies(ctx context.Context, snapshot *routing.Snapshot) error {
	siteID := snapshot.SiteID
	for _, resource := range snapshot.Resources {
		var exists bool
		if err := o.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_versions WHERE site_id=$1 AND group_key=$2 AND status IN ('published','applied'))`, siteID, resource.Group.Key).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := o.insertPrice(ctx, siteID, resource.Group.Key, resource.Group.SaleRatio, "沿用现有专属分组价格", "published", o.mutation.Bases[siteID]); err != nil {
				return err
			}
		}
	}
	if basis, ok := o.mutation.Bases[siteID]; ok && basis.Hash != "" {
		encoded, err := json.Marshal(basis.Values)
		if err != nil {
			return err
		}
		if _, err = o.tx.Exec(ctx, `UPDATE price_versions SET basis_hash=$2,billing_basis=$3 WHERE site_id=$1 AND status='published' AND basis_hash=''`, siteID, basis.Hash, encoded); err != nil {
			return err
		}
	}
	priceRows, err := o.tx.Query(ctx, `SELECT id,group_key,sale_ratio::text,basis_hash FROM price_versions WHERE site_id=$1 AND status IN ('published','applied') ORDER BY id`, siteID)
	if err != nil {
		return err
	}
	prices, err := pgx.CollectRows(priceRows, func(row pgx.CollectableRow) (planPrice, error) {
		var p planPrice
		err := row.Scan(&p.ID, &p.GroupKey, &p.Ratio, &p.Hash)
		return p, err
	})
	if err != nil {
		return err
	}
	byGroup := make(map[string]planPrice, len(prices))
	hash := ""
	for _, price := range prices {
		byGroup[price.GroupKey] = price
		snapshot.PriceVersionIDs = append(snapshot.PriceVersionIDs, price.ID)
		if price.Hash == "" {
			hash = strings.Repeat("0", 64)
			continue
		}
		if hash == "" {
			hash = price.Hash
		} else if hash != price.Hash {
			hash = strings.Repeat("0", 64)
		}
	}
	if hash == "" {
		hash = strings.Repeat("0", 64)
	}
	snapshot.BillingBasisHash = hash
	for i := range snapshot.Resources {
		if p, ok := byGroup[snapshot.Resources[i].Group.Key]; ok {
			snapshot.Resources[i].Group.SaleRatio = p.Ratio
		}
	}
	rows, err := o.tx.Query(ctx, `SELECT id,group_key,display_name,enabled,visible,version FROM site_strategies WHERE site_id=$1 ORDER BY group_key`, siteID)
	if err != nil {
		return err
	}
	type configuredStrategy struct {
		id               uuid.UUID
		key, name        string
		enabled, visible bool
		version          int64
	}
	strategies, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (configuredStrategy, error) {
		var result configuredStrategy
		err := row.Scan(&result.id, &result.key, &result.name, &result.enabled, &result.visible, &result.version)
		return result, err
	})
	if err != nil {
		return err
	}
	for _, strategy := range strategies {
		snapshot.StrategyVersions = append(snapshot.StrategyVersions, routing.StrategyReference{ID: strategy.id, Version: strategy.version})
		price, exists := byGroup[strategy.key]
		if !exists {
			continue
		}
		memberRows, err := o.tx.Query(ctx, `SELECT relation_id FROM strategy_members WHERE strategy_id=$1 ORDER BY relation_id`, strategy.id)
		if err != nil {
			return err
		}
		members, err := pgx.CollectRows(memberRows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		eligible := 0
		for i := range snapshot.Resources {
			resource := &snapshot.Resources[i]
			for _, member := range members {
				if resource.RelationID == member && strategy.enabled && resource.Channel.DesiredStatus == routing.DesiredEnabled {
					resource.Channel.ExtraGroupKeys = append(resource.Channel.ExtraGroupKeys, strategy.key)
					eligible++
				}
			}
		}
		snapshot.AutoGroups = append(snapshot.AutoGroups, routing.DesiredGroup{Key: strategy.key, DisplayName: strategy.name, SaleRatio: price.Ratio, Visible: strategy.enabled && strategy.visible && eligible > 0})
	}
	for i := range snapshot.Resources {
		sort.Strings(snapshot.Resources[i].Channel.ExtraGroupKeys)
	}
	sort.Slice(snapshot.StrategyVersions, func(i, j int) bool {
		return snapshot.StrategyVersions[i].ID.String() < snapshot.StrategyVersions[j].ID.String()
	})
	return nil
}

func (o *operationTx) enqueuePlan(ctx context.Context, id, siteID, relationID uuid.UUID) error {
	var operationID uuid.UUID
	err := o.tx.QueryRow(ctx, `INSERT INTO sync_operations(id,site_id,site_supplier_id,route_plan_id,status,created_at,updated_at)
        VALUES($1,$2,$3,$4,'pending',$5,$5) ON CONFLICT(route_plan_id) DO UPDATE SET status='pending',last_error_code=NULL,last_error_message=NULL,completed_at=NULL,updated_at=$5 RETURNING id`, uuid.New(), siteID, relationID, id, o.now).Scan(&operationID)
	if err != nil {
		return err
	}
	client, err := river.NewClient(riverpgxv5.New(nil), &river.Config{})
	if err != nil {
		return err
	}
	_, err = client.InsertTx(ctx, o.tx, jobs.ReconciliationArgs{OperationID: operationID.String()}, &river.InsertOpts{Queue: "reconciliation", MaxAttempts: 10})
	return err
}

func mutationReason(m domain.Mutation) string {
	switch v := m.Input.(type) {
	case domain.SiteInput:
		if v.Reason != "" {
			return v.Reason
		}
	case domain.SupplierInput:
		if v.Reason != "" {
			return v.Reason
		}
	case domain.CredentialInput:
		return v.Reason
	case domain.CredentialCancelInput:
		return v.Reason
	case domain.DeploymentInput:
		return v.Reason
	case domain.RelationInput:
		return v.Reason
	case domain.StrategyInput:
		return v.Reason
	case domain.PriceInput:
		return v.Reason
	case domain.RestoreInput:
		return v.Reason
	}
	return "运营确认同步"
}
