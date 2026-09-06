import type { RuntimeHealth } from "../../api/types";
import { Status } from "../../components/ui";

export function SystemOverview({ data }: { data: RuntimeHealth }) {
  const system = data.system;
  const jobs = system.facts.jobs;
  return (
    <section className="runtime-overview" aria-label="系统运行摘要">
      <div className="runtime-overview-item">
        <span>整体状态</span>
        <Status value={data.status} />
      </div>
      <div className="runtime-overview-item">
        <span>发布组合</span>
        <strong>{system.build_version}</strong>
        <small>{shortCommit(system.build_commit)}</small>
      </div>
      <div className="runtime-overview-item">
        <span>数据库</span>
        <strong>{system.facts.database_up ? "可用" : "不可用"}</strong>
        <small>迁移 {system.facts.migration_version}</small>
      </div>
      <div className="runtime-overview-item">
        <span>后台任务</span>
        <strong>{jobs.running} 执行中</strong>
        <small>
          {jobs.waiting} 等待 · {jobs.retryable} 重试 · {jobs.failed} 失败
        </small>
      </div>
      <div className="runtime-overview-item">
        <span>兼容清单</span>
        <strong>{system.compatibility_catalog_version}</strong>
        <small>{formatRuntimeTime(data.generated_at)}</small>
      </div>
    </section>
  );
}

export function FactCell({
  value,
  time,
  empty,
}: {
  value?: string;
  time?: string;
  empty: string;
}) {
  if (!time) return <span className="muted">{empty}</span>;
  return (
    <div className="state-with-detail">
      {value && <span className="cell-title">{value}</span>}
      <span className="cell-sub">{formatRuntimeTime(time)}</span>
    </div>
  );
}

export function formatRuntimeTime(value?: string): string {
  return value ? new Date(value).toLocaleString() : "尚无记录";
}

function shortCommit(value: string): string {
  return value === "unknown" ? "提交待确认" : value.slice(0, 12);
}
