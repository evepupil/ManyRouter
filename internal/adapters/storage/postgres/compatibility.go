package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/evepupil/ManyRouter/internal/application/compatibility"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/site"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (store *Store) GetCompatibilitySite(ctx context.Context, siteID uuid.UUID) (compatibility.SiteAccess, error) {
	row, err := store.queries.GetSite(ctx, siteID)
	if err != nil {
		return compatibility.SiteAccess{}, err
	}
	secret, err := store.queries.GetCredential(ctx, row.AdminCredentialID)
	if err != nil {
		return compatibility.SiteAccess{}, err
	}
	if secret.RevokedAt.Valid {
		return compatibility.SiteAccess{}, errors.New("site credential is revoked")
	}
	return compatibility.SiteAccess{
		ID: row.ID, Code: row.Code, Name: row.Name, BaseURL: row.NewApiBaseUrl,
		AdminUserID: row.AdminUserID, Credential: mapCredential(secret),
	}, nil
}

func (store *Store) ListCompatibilitySiteIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := store.pool.Query(ctx, `SELECT id FROM sites WHERE status='enabled' ORDER BY code,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]uuid.UUID, 0)
	for rows.Next() {
		var siteID uuid.UUID
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		result = append(result, siteID)
	}
	return result, rows.Err()
}

func (store *Store) ManagedSyncApproved(
	ctx context.Context,
	siteID uuid.UUID,
	capability reconciliation.ManagedSyncCapabilities,
) (bool, error) {
	var approved bool
	err := store.pool.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT mode='managed' AND verdict='compatible'
			       AND new_api_version=$2 AND contract_version=$3 AND database_type=$4
			FROM site_compatibility_checks WHERE site_id=$1
			ORDER BY checked_at DESC,id DESC LIMIT 1
		),false)
	`, siteID, capability.NewAPIVersion, capability.ContractVersion, capability.DatabaseType).Scan(&approved)
	return approved, err
}

func (store *Store) SaveCompatibilityCheck(
	ctx context.Context,
	report compatibility.Report,
	status site.CompatibilityStatus,
) error {
	capabilities, err := json.Marshal(report.Capabilities)
	if err != nil {
		return err
	}
	conflicts, err := json.Marshal(report.Conflicts)
	if err != nil {
		return err
	}
	reasons, err := json.Marshal(report.Reasons)
	if err != nil {
		return err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous string
	if err := tx.QueryRow(ctx, `SELECT compatibility_status FROM sites WHERE id=$1 FOR UPDATE`, report.SiteID).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO site_compatibility_checks(
			id,site_id,mode,verdict,catalog_version,new_api_version,contract_version,database_type,
			capabilities,state_hash,billing_basis_hash,conflicts,reasons,error_code,error_message,checked_by,checked_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, report.ID, report.SiteID, report.Mode, report.Verdict, report.CatalogVersion,
		report.NewAPIVersion, report.ContractVersion, report.DatabaseType, capabilities,
		report.StateHash, report.BillingBasisHash, conflicts, reasons, report.ErrorCode,
		report.ErrorMessage, report.CheckedBy, report.CheckedAt.UTC()); err != nil {
		return err
	}
	if previous != string(status) {
		if _, err := tx.Exec(ctx, `
			UPDATE sites SET compatibility_status=$2,version=version+1,updated_at=$3 WHERE id=$1
		`, report.SiteID, status, report.CheckedAt.UTC()); err != nil {
			return err
		}
	}
	actorType := "operator"
	if strings.HasPrefix(report.CheckedBy, "system:") {
		actorType = "system"
	}
	if err := insertAudit(ctx, store.queries.WithTx(tx), auditInput{
		ActorType: actorType, ActorID: report.CheckedBy, SiteID: &report.SiteID,
		ObjectType: "site_compatibility", ObjectID: report.ID.String(), Action: "site.compatibility_checked",
		Reason: "runtime_check", OldSummary: map[string]any{"compatibility_status": previous},
		NewSummary: map[string]any{
			"compatibility_status": status, "mode": report.Mode, "verdict": report.Verdict,
			"new_api_version": report.NewAPIVersion, "contract_version": report.ContractVersion,
			"database_type": report.DatabaseType, "reason_codes": compatibilityReasonCodes(report.Reasons),
		},
		Result: "succeeded", CreatedAt: report.CheckedAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) GetLatestCompatibilityCheck(ctx context.Context, siteID uuid.UUID) (compatibility.Report, error) {
	row := store.pool.QueryRow(ctx, latestCompatibilityQuery+` WHERE checks.site_id=$1 ORDER BY checks.checked_at DESC,checks.id DESC LIMIT 1`, siteID)
	return scanCompatibilityReport(row)
}

func (store *Store) ListLatestCompatibilityChecks(ctx context.Context) ([]compatibility.Report, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT DISTINCT ON (checks.site_id)
			checks.id,checks.site_id,sites.code,sites.name,checks.mode,checks.verdict,checks.catalog_version,
			checks.new_api_version,checks.contract_version,checks.database_type,checks.capabilities,
			checks.state_hash,checks.billing_basis_hash,checks.conflicts,checks.reasons,
			checks.error_code,checks.error_message,checks.checked_by,checks.checked_at
		FROM site_compatibility_checks checks
		JOIN sites ON sites.id=checks.site_id
		ORDER BY checks.site_id,checks.checked_at DESC,checks.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]compatibility.Report, 0)
	for rows.Next() {
		report, err := scanCompatibilityReport(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, report)
	}
	return result, rows.Err()
}

const latestCompatibilityQuery = `
	SELECT checks.id,checks.site_id,sites.code,sites.name,checks.mode,checks.verdict,checks.catalog_version,
	       checks.new_api_version,checks.contract_version,checks.database_type,checks.capabilities,
	       checks.state_hash,checks.billing_basis_hash,checks.conflicts,checks.reasons,
	       checks.error_code,checks.error_message,checks.checked_by,checks.checked_at
	FROM site_compatibility_checks checks
	JOIN sites ON sites.id=checks.site_id
`

type compatibilityScanner interface {
	Scan(...any) error
}

func scanCompatibilityReport(scanner compatibilityScanner) (compatibility.Report, error) {
	var report compatibility.Report
	var capabilitiesJSON, conflictsJSON, reasonsJSON []byte
	if err := scanner.Scan(
		&report.ID, &report.SiteID, &report.SiteCode, &report.SiteName, &report.Mode, &report.Verdict,
		&report.CatalogVersion, &report.NewAPIVersion, &report.ContractVersion, &report.DatabaseType,
		&capabilitiesJSON, &report.StateHash, &report.BillingBasisHash, &conflictsJSON, &reasonsJSON,
		&report.ErrorCode, &report.ErrorMessage, &report.CheckedBy, &report.CheckedAt,
	); err != nil {
		return compatibility.Report{}, err
	}
	if err := json.Unmarshal(capabilitiesJSON, &report.Capabilities); err != nil {
		return compatibility.Report{}, err
	}
	if err := json.Unmarshal(conflictsJSON, &report.Conflicts); err != nil {
		return compatibility.Report{}, err
	}
	if err := json.Unmarshal(reasonsJSON, &report.Reasons); err != nil {
		return compatibility.Report{}, err
	}
	report.CheckedAt = report.CheckedAt.UTC()
	return report, nil
}

func compatibilityReasonCodes(reasons []compatibility.Reason) []string {
	result := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		result = append(result, reason.Code)
	}
	return result
}

var _ compatibility.Store = (*Store)(nil)
var _ reconciliation.ManagedSyncApprovalStore = (*Store)(nil)
