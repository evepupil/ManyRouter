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

func (o *operationTx) saveSealed(ctx context.Context) error {
	c := o.mutation.Sealed
	if c == nil {
		return nil
	}
	_, err := o.tx.Exec(ctx, `INSERT INTO credentials(id,purpose,ciphertext,nonce,key_version,created_at) VALUES($1,$2,$3,$4,$5,$6)`, c.ID, string(c.Purpose), c.Ciphertext, c.Nonce, c.KeyVersion, o.now)
	return err
}

func (o *operationTx) saveSite(ctx context.Context, input domain.SiteInput) (any, error) {
	if err := o.saveSealed(ctx); err != nil {
		return nil, err
	}
	id := o.mutation.ID
	if o.mutation.Kind == "create_site" {
		id = uuid.New()
		_, err := o.tx.Exec(ctx, `INSERT INTO sites(id,code,name,new_api_base_url,admin_credential_id,admin_user_id,status,compatibility_status,version,created_at,updated_at)
            VALUES($1,$2,$3,$4,$5,$6,'enabled','unknown',1,$7,$7)`, id, input.Code, input.Name, input.NewAPIBaseURL, o.mutation.Sealed.ID, input.AdminUserID, o.now)
		if err != nil {
			return nil, err
		}
	} else {
		var previousURL string
		var previousCredentialID uuid.UUID
		var count int
		if err := o.tx.QueryRow(ctx, `SELECT new_api_base_url,admin_credential_id,(SELECT count(*) FROM site_suppliers WHERE site_id=s.id) FROM sites s WHERE id=$1`, id).Scan(&previousURL, &previousCredentialID, &count); err != nil {
			return nil, err
		}
		if previousURL != input.NewAPIBaseURL && count > 0 {
			return nil, fmt.Errorf("%w: 已有投放记录的站点不能替换地址，请新增目标站点后迁移", domain.ErrInvalid)
		}
		var credentialID any
		if o.mutation.Sealed != nil {
			credentialID = o.mutation.Sealed.ID
		}
		tag, err := o.tx.Exec(ctx, `UPDATE sites SET name=$2,new_api_base_url=$3,admin_user_id=$4,status=$5,admin_credential_id=COALESCE($6,admin_credential_id),version=version+1,updated_at=$7 WHERE id=$1 AND version=$8`, id, input.Name, input.NewAPIBaseURL, input.AdminUserID, input.Status, credentialID, o.now, input.Version)
		if err = requireUpdated(tag.RowsAffected(), err); err != nil {
			return nil, err
		}
		if o.mutation.Sealed != nil {
			if _, err = o.tx.Exec(ctx, `UPDATE credentials SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, previousCredentialID, o.now); err != nil {
				return nil, err
			}
		}
		if input.Status == "disabled" {
			delete(o.affected, id)
		}
	}
	if err := o.audit(ctx, id, "site", id, input.Reason); err != nil {
		return nil, err
	}
	return o.readResource(ctx, "sites", id)
}

func (o *operationTx) saveSupplier(ctx context.Context, input domain.SupplierInput) (any, error) {
	if err := o.saveSealed(ctx); err != nil {
		return nil, err
	}
	id := o.mutation.ID
	if o.mutation.Kind == "create_supplier" {
		id = uuid.New()
		_, err := o.tx.Exec(ctx, `INSERT INTO suppliers(id,code,name,protocol,upstream_base_url,credential_id,credential_version,status,version,created_at,updated_at)
            VALUES($1,$2,$3,'openai_compatible',$4,$5,1,'enabled',1,$6,$6)`, id, input.Code, input.Name, input.BaseURL, o.mutation.Sealed.ID, o.now)
		if err != nil {
			return nil, err
		}
	} else {
		tag, err := o.tx.Exec(ctx, `UPDATE suppliers SET name=$2,upstream_base_url=$3,status=$4,version=version+1,updated_at=$5 WHERE id=$1 AND version=$6 AND (pending_credential_id IS NULL OR $4::text='disabled')`, id, input.Name, input.BaseURL, input.Status, o.now, input.Version)
		if err = requireUpdated(tag.RowsAffected(), err); err != nil {
			return nil, err
		}
	}
	modelNames := make([]string, 0, len(input.Models))
	for _, m := range input.Models {
		modelNames = append(modelNames, m.Model)
		if _, err := o.tx.Exec(ctx, `INSERT INTO supplier_models(supplier_id,model,upstream_model,input_price,output_price,currency,enabled,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)
			ON CONFLICT (supplier_id,model) DO UPDATE SET
				upstream_model=EXCLUDED.upstream_model,
				input_price=EXCLUDED.input_price,
				output_price=EXCLUDED.output_price,
				currency=EXCLUDED.currency,
				enabled=EXCLUDED.enabled,
				updated_at=EXCLUDED.updated_at`, id, m.Model, m.UpstreamModel, m.InputPrice, m.OutputPrice, m.Currency, m.Enabled, o.now); err != nil {
			return nil, err
		}
	}
	if o.mutation.Kind != "create_supplier" {
		if _, err := o.tx.Exec(ctx, `DELETE FROM supplier_models WHERE supplier_id=$1 AND NOT (model=ANY($2::text[]))`, id, modelNames); err != nil {
			return nil, err
		}
	}
	if err := o.audit(ctx, uuid.Nil, "supplier", id, input.Reason); err != nil {
		return nil, err
	}
	return o.readResource(ctx, "suppliers", id)
}

func (o *operationTx) rotateCredential(ctx context.Context, input domain.CredentialInput) error {
	if err := o.saveSealed(ctx); err != nil {
		return err
	}
	id := o.mutation.ID
	var replacedCredentialID *uuid.UUID
	var existing uuid.UUID
	if err := o.tx.QueryRow(ctx, `SELECT pending_credential_id FROM suppliers WHERE id=$1 AND pending_credential_id IS NOT NULL`, id).Scan(&existing); err == nil {
		replacedCredentialID = &existing
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	tag, err := o.tx.Exec(ctx, `UPDATE suppliers SET pending_credential_id=$2,pending_credential_version=COALESCE(pending_credential_version,credential_version)+1,version=version+1,updated_at=$3
        WHERE id=$1 AND version=$4`, id, o.mutation.Sealed.ID, o.now, input.Version)
	if err = requireUpdated(tag.RowsAffected(), err); err != nil {
		return err
	}
	if replacedCredentialID != nil {
		if _, err = o.tx.Exec(ctx, `UPDATE credentials SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, *replacedCredentialID, o.now); err != nil {
			return err
		}
	}
	if len(o.affected) == 0 {
		if _, err = o.tx.Exec(ctx, `UPDATE suppliers SET credential_id=pending_credential_id,credential_version=pending_credential_version,pending_credential_id=NULL,pending_credential_version=NULL WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return o.audit(ctx, uuid.Nil, "supplier", id, input.Reason)
}

func (o *operationTx) cancelCredential(ctx context.Context, input domain.CredentialCancelInput) error {
	var candidateID uuid.UUID
	if err := o.tx.QueryRow(ctx, `SELECT pending_credential_id FROM suppliers WHERE id=$1 AND pending_credential_id IS NOT NULL`, o.mutation.ID).Scan(&candidateID); err != nil {
		return err
	}
	tag, err := o.tx.Exec(ctx, `UPDATE suppliers SET pending_credential_id=NULL,pending_credential_version=NULL,version=version+1,updated_at=$3 WHERE id=$1 AND version=$2 AND pending_credential_id IS NOT NULL`, o.mutation.ID, input.Version, o.now)
	if err = requireUpdated(tag.RowsAffected(), err); err != nil {
		return err
	}
	if _, err = o.tx.Exec(ctx, `UPDATE credentials SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, candidateID, o.now); err != nil {
		return err
	}
	return o.audit(ctx, uuid.Nil, "supplier", o.mutation.ID, input.Reason)
}

func (o *operationTx) readResource(ctx context.Context, kind string, id uuid.UUID) (json.RawMessage, error) {
	source, ok := operationReadQueries[kind]
	if !ok {
		return nil, domain.ErrNotFound
	}
	var raw []byte
	err := o.tx.QueryRow(ctx, `SELECT data FROM (`+source+`) records WHERE data->>'id'=$1`, id.String()).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return sanitizeOperationJSON(raw)
}
