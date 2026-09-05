import { useState, type FormEvent } from "react";
import { ShieldCheck } from "lucide-react";
import { useAction } from "../../api/hooks";
import type { EvaluationRun, TrustedReferenceInput } from "../../api/types";
import {
  Button,
  DateTime,
  Field,
  Input,
  Modal,
  Notice,
  Select,
} from "../../components/ui";
import { trustNames } from "./labels";

export function ReferenceEditor({
  run,
  onClose,
  onSubmitted,
}: {
  run: EvaluationRun;
  onClose: () => void;
  onSubmitted: () => void;
}) {
  const [trust, setTrust] =
    useState<TrustedReferenceInput["trust"]>("operator_trusted");
  const [validDays, setValidDays] = useState("7");
  const [reason, setReason] = useState("");
  const action = useAction(() => {
    onSubmitted();
    onClose();
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `evaluation-runs/${run.id}/reference`,
      body: { trust, valid_days: Number(validDays), reason },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`设为可信参考 · ${run.model}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <dl className="detail-list">
          <dt>供应商</dt>
          <dd>{run.supplier_name || run.supplier_id}</dd>
          <dt>模型</dt>
          <dd>{run.model}</dd>
          <dt>测评完成</dt>
          <dd>
            <DateTime value={run.completed_at} />
          </dd>
        </dl>
        <Field label="参考级别">
          <Select
            value={trust}
            onChange={(event) =>
              setTrust(event.target.value as TrustedReferenceInput["trust"])
            }
          >
            {Object.entries(trustNames).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="有效天数">
          <Input
            type="number"
            required
            min="1"
            max="90"
            step="1"
            value={validDays}
            onChange={(event) => setValidDays(event.target.value)}
          />
        </Field>
        <Field label="设为参考的原因">
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
            取消
          </Button>
          <Button
            type="submit"
            variant="primary"
            icon={<ShieldCheck />}
            pending={action.isPending}
          >
            保存可信参考
          </Button>
        </div>
      </form>
    </Modal>
  );
}
