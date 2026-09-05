import { useState, type FormEvent } from "react";
import { XCircle } from "lucide-react";
import type { Supplier } from "../../api/types";
import { useAction } from "../../api/hooks";
import { Button, Field, Input, Modal, Notice } from "../../components/ui";

export function CancelCredential({
  supplier,
  onClose,
}: {
  supplier: Supplier;
  onClose: () => void;
}) {
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `suppliers/${supplier.id}/credentials/cancel`,
      body: { version: supplier.version, reason },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`取消候选密钥 · ${supplier.name}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <dl className="detail-list">
          <dt>生效版本</dt>
          <dd>{supplier.credential_version}</dd>
          <dt>待同步版本</dt>
          <dd>{supplier.pending_credential_version}</dd>
        </dl>
        <Field label="取消原因">
          <Input
            required
            maxLength={500}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
        <Notice error={action.error} />
        <div className="form-actions">
          <Button onClick={onClose} disabled={action.isPending}>
            返回
          </Button>
          <Button
            type="submit"
            variant="danger"
            icon={<XCircle />}
            pending={action.isPending}
          >
            取消候选密钥
          </Button>
        </div>
      </form>
    </Modal>
  );
}
