import { useState, type FormEvent } from "react";
import { Beaker } from "lucide-react";
import { useAction } from "../../api/hooks";
import type { EvaluationPurpose, Relation, Supplier } from "../../api/types";
import {
  Button,
  Empty,
  Field,
  Input,
  Loading,
  Modal,
  Notice,
  Select,
} from "../../components/ui";
import { purposeNames } from "./labels";

export interface EvaluationTarget {
  key: string;
  supplierId: string;
  supplierName: string;
  model: string;
}

export function EvaluationEditor({
  targets,
  loading,
  error,
  onRetry,
  onSettled,
  onClose,
  onSubmitted,
}: {
  targets: EvaluationTarget[];
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  onSettled: () => void;
  onClose: () => void;
  onSubmitted: () => void;
}) {
  const [targetKey, setTargetKey] = useState("");
  const [purpose, setPurpose] = useState<EvaluationPurpose>("health");
  const [reason, setReason] = useState("");
  const action = useAction(() => {
    onSubmitted();
    onClose();
  });
  const target = targets.find((item) => item.key === targetKey);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!target) return;
    action.mutate(
      {
        path: "evaluation-runs",
        body: {
          supplier_id: target.supplierId,
          model: target.model,
          purpose,
          target_kind: "supplier_direct",
          reason,
        },
      },
      { onSettled },
    );
  };
  return (
    <Modal open busy={action.isPending} onClose={onClose} title="发起主动测评">
      {loading ? (
        <Loading />
      ) : error ? (
        <div className="form-stack">
          <Notice error={error} />
          <Button onClick={onRetry}>重新读取可测评模型</Button>
        </div>
      ) : targets.length === 0 ? (
        <Empty title="当前站点暂无可测评模型" />
      ) : (
        <form className="form-stack" onSubmit={submit}>
          <Field label="测评目标">
            <Select
              required
              value={targetKey}
              onChange={(event) => setTargetKey(event.target.value)}
            >
              <option value="">选择供应商和模型</option>
              {targets.map((item) => (
                <option key={item.key} value={item.key}>
                  {item.supplierName} · {item.model}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="测评用途">
            <Select
              value={purpose}
              onChange={(event) =>
                setPurpose(event.target.value as EvaluationPurpose)
              }
            >
              {Object.entries(purposeNames).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="发起原因">
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
              icon={<Beaker />}
              pending={action.isPending}
              disabled={!target}
            >
              提交测评
            </Button>
          </div>
        </form>
      )}
    </Modal>
  );
}

export function evaluationTargets(
  relations: Relation[],
  suppliers: Supplier[],
): EvaluationTarget[] {
  const supplierByID = new Map(
    suppliers.map((supplier) => [supplier.id, supplier]),
  );
  const targets: EvaluationTarget[] = [];
  for (const relation of relations) {
    const supplier = supplierByID.get(relation.supplier_id);
    if (!supplier || supplier.status !== "enabled") continue;
    for (const model of supplier.models) {
      if (!model.enabled) continue;
      targets.push({
        key: `${supplier.id}:${model.model}`,
        supplierId: supplier.id,
        supplierName: supplier.name,
        model: model.model,
      });
    }
  }
  return targets.sort((left, right) =>
    `${left.supplierName}\u0000${left.model}`.localeCompare(
      `${right.supplierName}\u0000${right.model}`,
      "zh-CN",
    ),
  );
}
