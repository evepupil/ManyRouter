import { useDeferredValue, useState } from "react";
import { Eye, RefreshCw, ShieldAlert } from "lucide-react";
import type { ScoreInsight, ShadowRecommendation } from "../../api/types";
import { useAction, useList } from "../../api/hooks";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Badge,
  Button,
  DateTime,
  IconButton,
  Modal,
  Notice,
} from "../../components/ui";
import {
  authenticityLabel,
  confidenceLabel,
  eligibilityLabel,
  reasonName,
  recommendationLabel,
  strategyName,
} from "./labels";
import { scoreWindowEvidence } from "./score-explanation";

export function ScoreView({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [viewing, setViewing] = useState<ScoreInsight | null>(null);
  const [success, setSuccess] = useState("");
  const model = useDeferredValue(search.trim());
  const query = useList<ScoreInsight>("score-insights", {
    site_id: siteId,
    model: model || undefined,
    offset,
  });
  const refreshScores = useAction(() => {
    setSuccess("评分已刷新");
  });
  const rows = (query.data?.items ?? []).map((item) => ({
    ...item,
    id: item.snapshot_id,
  }));

  return (
    <div className="form-stack observability-score">
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
        <Button
          variant="primary"
          icon={<RefreshCw />}
          pending={refreshScores.isPending}
          onClick={() => {
            setSuccess("");
            refreshScores.reset();
            refreshScores.mutate(
              { path: "score-runs", body: {} },
              {
                onSettled: () => {
                  void query.refetch();
                },
              },
            );
          }}
        >
          刷新全部评分
        </Button>
      </Toolbar>
      <Notice error={refreshScores.error} success={success} />
      <DataTable
        items={rows}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无评分快照"
        columns={[
          {
            key: "target",
            title: "供应商 / 模型",
            render: (insight) => (
              <>
                <div className="cell-title">{insight.supplier_name}</div>
                <div className="cell-sub">{insight.model}</div>
              </>
            ),
          },
          {
            key: "total",
            title: "均衡综合",
            className: "numeric",
            render: (insight) => (
              <ScoreNumber value={insight.total_score} strong />
            ),
          },
          {
            key: "price",
            title: "价格",
            className: "numeric",
            render: (insight) => <ScoreNumber value={insight.price_score} />,
          },
          {
            key: "latency",
            title: "延迟",
            className: "numeric",
            render: (insight) => <ScoreNumber value={insight.latency_score} />,
          },
          {
            key: "sla",
            title: "SLA",
            className: "numeric",
            render: (insight) => <ScoreNumber value={insight.sla_score} />,
          },
          {
            key: "quality",
            title: "质量",
            className: "numeric",
            render: (insight) => <ScoreNumber value={insight.quality_score} />,
          },
          {
            key: "state",
            title: "结论",
            render: (insight) => {
              const eligibility = eligibilityLabel(insight.eligibility);
              const confidence = confidenceLabel(insight.confidence);
              const authenticity = authenticityLabel(
                insight.authenticity_verdict,
              );
              return (
                <div className="inline-badges">
                  <Badge tone={eligibility.tone}>{eligibility.label}</Badge>
                  <Badge tone={confidence.tone}>
                    可信度 {confidence.label}
                  </Badge>
                  <Badge tone={authenticity.tone}>{authenticity.label}</Badge>
                </div>
              );
            },
          },
          {
            key: "samples",
            title: "样本",
            className: "numeric",
            render: (insight) => (
              <>
                <div>
                  24h 有效尝试 {insight.passive_samples.toLocaleString("zh-CN")}
                </div>
                <div className="cell-sub">
                  质量检查 {insight.active_samples.toLocaleString("zh-CN")}
                </div>
              </>
            ),
          },
          {
            key: "facts",
            title: "事实截止",
            render: (insight) => <DateTime value={insight.facts_through} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (insight) => (
              <div className="cell-actions">
                <IconButton
                  label={`查看 ${insight.supplier_name} ${insight.model} 的评分详情`}
                  onClick={() => setViewing(insight)}
                >
                  <Eye size={16} />
                </IconButton>
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
      {viewing && (
        <ScoreDetail insight={viewing} onClose={() => setViewing(null)} />
      )}
    </div>
  );
}

function ScoreNumber({
  value,
  strong = false,
}: {
  value?: number | null;
  strong?: boolean;
}) {
  if (value === null || value === undefined)
    return <span className="muted">未评分</span>;
  return (
    <span
      className={strong ? "score-number score-number-strong" : "score-number"}
    >
      {value.toFixed(1)}
    </span>
  );
}

function ScoreDetail({
  insight,
  onClose,
}: {
  insight: ScoreInsight;
  onClose: () => void;
}) {
  const recommendations = insight.recommendations.map((item) => ({
    ...item,
    id: item.strategy_kind,
  }));
  const windows = scoreWindowEvidence(insight.explanation);
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={`评分详情 · ${insight.supplier_name} · ${insight.model}`}
    >
      <div className="form-stack">
        <div className="score-metrics" aria-label="评分分项">
          <Metric label="均衡综合" value={insight.total_score} />
          <Metric label="价格" value={insight.price_score} />
          <Metric label="延迟" value={insight.latency_score} />
          <Metric label="SLA" value={insight.sla_score} />
          <Metric label="质量" value={insight.quality_score} />
        </div>
        <dl className="detail-list">
          <dt>站点编号</dt>
          <dd>{insight.site_id}</dd>
          <dt>供应商编号</dt>
          <dd>{insight.supplier_id}</dd>
          <dt>24h 有效尝试</dt>
          <dd>{insight.passive_samples.toLocaleString("zh-CN")}</dd>
          <dt>质量检查</dt>
          <dd>{insight.active_samples.toLocaleString("zh-CN")}</dd>
          <dt>事实截止</dt>
          <dd>
            <DateTime value={insight.facts_through} />
          </dd>
          <dt>评分规则</dt>
          <dd>{insight.policy_version}</dd>
          <dt>评分窗口</dt>
          <dd>
            <DateTime value={insight.window_start} /> 至{" "}
            <DateTime value={insight.window_end} />
          </dd>
          <dt>生成时间</dt>
          <dd>
            <DateTime value={insight.created_at} />
          </dd>
        </dl>
        {insight.hard_reasons.length > 0 && (
          <div className="notice notice-error" role="note">
            <ShieldAlert aria-hidden="true" />
            <span>{insight.hard_reasons.map(reasonName).join("；")}</span>
          </div>
        )}
        <section className="detail-section">
          <h3>窗口依据</h3>
          <DataTable
            items={windows}
            empty="该快照未记录窗口依据"
            columns={windowColumns}
          />
        </section>
        <section className="detail-section">
          <h3>影子建议</h3>
          <DataTable
            items={recommendations}
            empty="暂无影子建议"
            columns={recommendationColumns}
          />
        </section>
      </div>
    </Modal>
  );
}

const windowNames: Record<string, string> = {
  "15m": "近 15 分钟",
  "1h": "近 1 小时",
  "24h": "近 24 小时",
};

const windowColumns = [
  {
    key: "window",
    title: "窗口",
    render: (item: ReturnType<typeof scoreWindowEvidence>[number]) =>
      windowNames[item.window] ?? item.window,
  },
  {
    key: "success",
    title: "成功 / 有效尝试",
    className: "numeric",
    render: (item: ReturnType<typeof scoreWindowEvidence>[number]) =>
      `${item.successes.toLocaleString("zh-CN")} / ${item.attempts.toLocaleString("zh-CN")}`,
  },
  {
    key: "ttft",
    title: "首字 P50 / P95",
    className: "numeric",
    render: (item: ReturnType<typeof scoreWindowEvidence>[number]) =>
      formatPairMillis(item.ttftP50, item.ttftP95),
  },
  {
    key: "recovery",
    title: "最长恢复",
    className: "numeric",
    render: (item: ReturnType<typeof scoreWindowEvidence>[number]) =>
      formatMillis(item.recoveryMillis),
  },
  {
    key: "evidence",
    title: "证据",
    render: (item: ReturnType<typeof scoreWindowEvidence>[number]) => (
      <Badge tone={item.complete ? "success" : "warning"}>
        {item.complete ? "完整" : "待补齐"}
      </Badge>
    ),
  },
];

function formatPairMillis(left?: number, right?: number): string {
  if (left === undefined || right === undefined) return "未记录";
  return `${formatMillis(left)} / ${formatMillis(right)}`;
}

function formatMillis(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(1)} 秒`;
  return `${Math.round(value)} 毫秒`;
}

const recommendationColumns = [
  {
    key: "strategy",
    title: "Auto",
    render: (item: ShadowRecommendation) => strategyName(item.strategy_kind),
  },
  {
    key: "action",
    title: "建议",
    render: (item: ShadowRecommendation) => {
      const state = recommendationLabel(item.action);
      return <Badge tone={state.tone}>{state.label}</Badge>;
    },
  },
  {
    key: "member",
    title: "当前成员",
    render: (item: ShadowRecommendation) =>
      item.current_member ? "名单内" : "名单外",
  },
  {
    key: "score",
    title: "策略分",
    className: "numeric",
    render: (item: ShadowRecommendation) => <ScoreNumber value={item.score} />,
  },
  {
    key: "confidence",
    title: "可信度",
    render: (item: ShadowRecommendation) => {
      const state = confidenceLabel(item.confidence);
      return <Badge tone={state.tone}>{state.label}</Badge>;
    },
  },
  {
    key: "reasons",
    title: "原因",
    render: (item: ShadowRecommendation) => (
      <span className="cell-sub">
        {item.reasons.length > 0
          ? item.reasons.map(reasonName).join("；")
          : "未记录"}
      </span>
    ),
  },
];

function Metric({ label, value }: { label: string; value?: number | null }) {
  return (
    <div className="score-metric">
      <span>{label}</span>
      <ScoreNumber value={value} strong />
    </div>
  );
}
