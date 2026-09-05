package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/google/uuid"
)

// Only these explicit projections are exposed to operators; credential storage is never serialized.
var operationReadQueries = map[string]string{
	"sites": `SELECT to_jsonb(s)-'admin_credential_id' AS data FROM sites s`,
	"suppliers": `SELECT (to_jsonb(s)-'credential_id'-'pending_credential_id') || jsonb_build_object('models',
        COALESCE((SELECT jsonb_agg(jsonb_build_object('model',m.model,'upstream_model',m.upstream_model,
        'input_price',m.input_price::text,'output_price',m.output_price::text,'currency',m.currency,'enabled',m.enabled) ORDER BY m.model)
        FROM supplier_models m WHERE m.supplier_id=s.id),'[]'::jsonb)) AS data FROM suppliers s`,
	"relations": `SELECT to_jsonb(r)||jsonb_build_object('sale_ratio',r.sale_ratio::text,'site_name',st.name,'supplier_name',sp.name,
        'credential_version',ch.last_confirmed_credential_version,'last_confirmed_enabled',ch.last_confirmed_enabled,
        'external_channel_id',ch.external_channel_id) AS data FROM site_suppliers r
        JOIN sites st ON st.id=r.site_id JOIN suppliers sp ON sp.id=r.supplier_id
        LEFT JOIN site_supplier_channels ch ON ch.site_supplier_id=r.id`,
	"strategies": `SELECT to_jsonb(s)||jsonb_build_object('member_relation_ids',COALESCE((SELECT jsonb_agg(m.relation_id ORDER BY m.relation_id)
        FROM strategy_members m WHERE m.strategy_id=s.id),'[]'::jsonb), 'sale_ratio',(SELECT p.sale_ratio::text FROM price_versions p
        WHERE p.site_id=s.site_id AND p.group_key=s.group_key AND p.status IN ('published','applied'))) AS data FROM site_strategies s`,
	"prices": `SELECT to_jsonb(p)||jsonb_build_object('sale_ratio',p.sale_ratio::text,'is_last_confirmed',p.applied_at IS NOT NULL AND NOT EXISTS(
        SELECT 1 FROM price_versions newer WHERE newer.site_id=p.site_id AND newer.group_key=p.group_key AND newer.applied_at>p.applied_at)) AS data FROM price_versions p`,
	"plans": `SELECT to_jsonb(p)||jsonb_build_object('previous_snapshot',(SELECT previous.snapshot FROM route_plan_versions previous WHERE previous.id=p.previous_plan_id)) AS data FROM route_plan_versions p`,
	"sync-operations": `SELECT to_jsonb(o)||jsonb_build_object('steps',COALESCE((SELECT jsonb_agg(to_jsonb(st) ORDER BY st.started_at,st.step_key)
        FROM sync_steps st WHERE st.operation_id=o.id),'[]'::jsonb)) AS data FROM sync_operations o`,
	"audit": `SELECT to_jsonb(a) AS data FROM audit_events a`,
}

func (s *Store) ListOperations(ctx context.Context, kind string, filter domain.Filter) (domain.Page, error) {
	source, ok := operationReadQueries[kind]
	if !ok {
		return domain.Page{}, domain.ErrNotFound
	}
	query := `WITH records AS (` + source + `), filtered AS (SELECT data FROM records WHERE
        ($1::text='' OR data::text ILIKE '%'||$1||'%') AND
        ($2::text='' OR data->>'site_id'=$2 OR ($6::text='sites' AND data->>'id'=$2)) AND
        ($3::text='' OR data->>'supplier_id'=$3))
        SELECT COALESCE((SELECT jsonb_agg(page.data) FROM (SELECT data FROM filtered ORDER BY data->>'created_at' DESC,data->>'id'
        LIMIT $4 OFFSET $5) page),'[]'::jsonb),(SELECT count(*) FROM filtered)`
	var raw []byte
	var total int64
	err := s.pool.QueryRow(ctx, query, filter.Query, uuidText(filter.SiteID), uuidText(filter.SupplierID), filter.Limit, filter.Offset, kind).Scan(&raw, &total)
	if err != nil {
		return domain.Page{}, err
	}
	items := make([]json.RawMessage, 0)
	if err := json.Unmarshal(raw, &items); err != nil {
		return domain.Page{}, err
	}
	for index := range items {
		items[index], err = sanitizeOperationJSON(items[index])
		if err != nil {
			return domain.Page{}, err
		}
	}
	return domain.Page{Items: items, Total: total, Offset: filter.Offset, Limit: filter.Limit}, nil
}

func (s *Store) GetOperationResource(ctx context.Context, kind string, id uuid.UUID) (json.RawMessage, error) {
	source, ok := operationReadQueries[kind]
	if !ok {
		return nil, domain.ErrNotFound
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM (`+source+`) records WHERE data->>'id'=$1`, id.String()).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return sanitizeOperationJSON(raw)
}

func (s *Store) GetSiteAccess(ctx context.Context, id uuid.UUID) (domain.SiteAccess, error) {
	row, err := s.queries.GetSite(ctx, id)
	if err != nil {
		return domain.SiteAccess{}, err
	}
	secret, err := s.queries.GetCredential(ctx, row.AdminCredentialID)
	if err != nil {
		return domain.SiteAccess{}, err
	}
	return domain.SiteAccess{BaseURL: row.NewApiBaseUrl, AdminUserID: row.AdminUserID, Credential: mapCredential(secret)}, nil
}

func (s *Store) GetSupplierAccess(ctx context.Context, id uuid.UUID) (domain.SupplierAccess, error) {
	var result domain.SupplierAccess
	err := s.pool.QueryRow(ctx, `SELECT upstream_base_url,version,COALESCE((SELECT upstream_model FROM supplier_models WHERE supplier_id=s.id AND enabled ORDER BY model LIMIT 1),'') FROM suppliers s WHERE id=$1`, id).Scan(&result.BaseURL, &result.Version, &result.TestModel)
	if err != nil {
		return result, err
	}
	if result.TestModel == "" {
		return result, fmt.Errorf("%w: 供应商没有启用的模型", domain.ErrInvalid)
	}
	row, err := s.queries.GetSupplier(ctx, id)
	if err != nil {
		return result, err
	}
	secret, err := s.queries.GetCredential(ctx, row.CredentialID)
	if err != nil {
		return result, err
	}
	result.Credential = mapCredential(secret)
	return result, nil
}

func uuidText(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func sanitizeOperationJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	removeCredentialReferences(value)
	return json.Marshal(value)
}

func removeCredentialReferences(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch key {
			case "credential_id", "admin_credential_id", "pending_credential_id", "ciphertext", "nonce", "password_hash", "token_hash", "csrf_hash", "setup_token", "api_key", "upstream_api_key", "new_api_access_token":
				delete(typed, key)
			default:
				removeCredentialReferences(item)
			}
		}
	case []any:
		for _, item := range typed {
			removeCredentialReferences(item)
		}
	}
}
