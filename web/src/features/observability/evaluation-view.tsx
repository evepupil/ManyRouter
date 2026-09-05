import { useDeferredValue, useMemo, useState } from "react";
import { Beaker, ShieldCheck } from "lucide-react";
import { useList } from "../../api/hooks";
import type {
  EvaluationPurpose,
  EvaluationRun,
  Relation,
  Supplier,
} from "../../api/types";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Badge,
  Button,
  DateTime,
  IconButton,
  Notice,
  Select,
} from "../../components/ui";
import { EvaluationEditor, evaluationTargets } from "./evaluation-editor";
import { evaluationStatusLabel, purposeNames } from "./labels";
import { ReferenceEditor } from "./reference-editor";

export function EvaluationView({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [purpose, setPurpose] = useState<"" | EvaluationPurpose>("");
  const [offset, setOffset] = useState(0);
  const [creating, setCreating] = useState(false);
  const [promoting, setPromoting] = useState<EvaluationRun | null>(null);
  const [success, setSuccess] = useState("");
  const model = useDeferredValue(search.trim());
  const query = useList<EvaluationRun>(
    "evaluation-runs",
    {
      site_id: siteId,
      model: model || undefined,
      purpose: purpose || undefined,
      offset,
    },
    { poll: true },
  );
  const relations = useList<Relation>("relations", {
    site_id: siteId,
    limit: 100,
  });
  const suppliers = useList<Supplier>("suppliers", { limit: 100 });
  const targets = useMemo(
    () =>
      evaluationTargets(
        relations.data?.items ?? [],
        suppliers.data?.items ?? [],
      ),
    [relations.data?.items, suppliers.data?.items],
  );

  return (
    <div className="form-stack observability-evaluation">
      <Toolbar
        search={search}
        onSearch={(value) => {
          setSearch(value);
          setOffset(0);
        }}
        onRefresh={() => {
          void query.refetch();
        }}
        placeholder="精确筛选模型"
      >
        <Select
          aria-label="测评用途"
          value={purpose}
          onChange={(event) => {
            setPurpose(event.target.value as "" | EvaluationPurpose);
            setOffset(0);
          }}
        >
          <option value="">全部用途</option>
          {Object.entries(purposeNames).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
        <Button
          variant="primary"
          icon={<Beaker />}
          onClick={() => {
            setSuccess("");
            setCreating(true);
          }}
        >
          发起测评
        </Button>
      </Toolbar>
      <Notice success={success} />
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无主动测评"
        columns={[
          {
            key: "target",
            title: "供应商 / 模型",
            render: (run) => (
              <>
                <div className="cell-title">
                  {run.supplier_name || run.supplier_id}
                </div>
                <div className="cell-sub">{run.model}</div>
              </>
            ),
          },
          {
            key: "purpose",
            title: "用途",
            render: (run) => (
              <>
                <div>{purposeNames[run.purpose]}</div>
                <div className="cell-sub">
                  {run.target_kind === "supplier_direct"
                    ? "供应商直连 · 共享证据"
                    : "站点链路"}
                </div>
              </>
            ),
          },
          {
            key: "progress",
            title: "进度",
            render: (run) => <EvaluationProgress run={run} />,
          },
          {
            key: "status",
            title: "状态",
            render: (run) => {
              const state = evaluationStatusLabel(run.status);
              return (
                <div className="state-with-detail">
                  <Badge tone={state.tone}>{state.label}</Badge>
                  {(run.error_message || run.error_code) && (
                    <span className="cell-sub">
                      {run.error_message || run.error_code}
                    </span>
                  )}
                </div>
              );
            },
          },
          {
            key: "reason",
            title: "请求原因",
            render: (run) => (
              <span className="cell-sub">{run.request_reason}</span>
            ),
          },
          {
            key: "version",
            title: "版本",
            render: (run) => (
              <>
                <div className="cell-sub">{run.suite_version}</div>
                <div className="cell-sub">{run.algorithm_version}</div>
              </>
            ),
          },
          {
            key: "requested",
            title: "发起时间",
            render: (run) => <DateTime value={run.requested_at} />,
          },
          {
            key: "completed",
            title: "完成时间",
            render: (run) => <DateTime value={run.completed_at} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (run) => (
              <div className="cell-actions">
                {canPromoteReference(run) ? (
                  <IconButton
                    label={`将 ${run.supplier_name || run.supplier_id} ${run.model} 设为可信参考`}
                    onClick={() => {
                      setSuccess("");
                      setPromoting(run);
                    }}
                  >
                    <ShieldCheck size={16} />
                  </IconButton>
                ) : null}
              </div>
            ),
          },
        ]}
      />
      <Pagination
        total={query.data?.total ?? 0}
        offset={offset}
        onChange={setOffset}
      />
      {creating && (
        <EvaluationEditor
          targets={targets}
          loading={relations.isPending || suppliers.isPending}
          error={relations.error ?? suppliers.error}
          onRetry={() => {
            void relations.refetch();
            void suppliers.refetch();
          }}
          onSettled={() => {
            void query.refetch();
          }}
          onClose={() => setCreating(false)}
          onSubmitted={() => setSuccess("主动测评已提交")}
        />
      )}
      {promoting && (
        <ReferenceEditor
          run={promoting}
          onClose={() => setPromoting(null)}
          onSubmitted={() => setSuccess("可信参考已保存")}
        />
      )}
    </div>
  );
}

function EvaluationProgress({ run }: { run: EvaluationRun }) {
  return (
    <div className="evaluation-progress">
      <span className="numeric">
        {run.completed_samples.toLocaleString("zh-CN")} /{" "}
        {run.planned_samples.toLocaleString("zh-CN")}
      </span>
      <progress
        aria-label={`${purposeNames[run.purpose]}进度`}
        max={run.planned_samples}
        value={Math.min(run.completed_samples, run.planned_samples)}
      />
    </div>
  );
}

function canPromoteReference(run: EvaluationRun): boolean {
  return (
    run.status === "succeeded" &&
    run.purpose === "authenticity" &&
    run.target_kind === "supplier_direct"
  );
}
