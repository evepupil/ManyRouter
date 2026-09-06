import type { RuntimeSite } from "../../api/types";
import { Modal, Status } from "../../components/ui";
import { formatRuntimeTime } from "./runtime-health-summary";

export function RuntimeDetail({
  site,
  onClose,
}: {
  site: RuntimeSite;
  onClose: () => void;
}) {
  const compatibility = site.compatibility;
  return (
    <Modal open onClose={onClose} title={`${site.site_name} · 运行详情`} wide>
      <div className="form-stack runtime-detail">
        <div className="inline-badges">
          <Status value={site.status} />
          <Status value={site.site_status} />
          {compatibility && <Status value={compatibility.mode} />}
        </div>
        {site.reasons.length > 0 && (
          <section className="detail-section">
            <h2>待处理</h2>
            <ul className="runtime-reason-list">
              {site.reasons.map((reason) => (
                <li key={reason.code}>
                  <strong>{reason.message}</strong>
                  {reason.action && <span>{reason.action}</span>}
                </li>
              ))}
            </ul>
          </section>
        )}
        <DetailSection
          title="兼容"
          values={[
            [
              "结论",
              compatibility ? statusText(compatibility.verdict) : "尚未检查",
            ],
            [
              "同步方式",
              compatibility ? statusText(compatibility.mode) : "尚未确认",
            ],
            ["New API", compatibility?.new_api_version || "尚未确认"],
            ["同步合同", compatibility?.contract_version || "旧接口"],
            ["数据库", compatibility?.database_type || "尚未确认"],
            ["最近检查", formatRuntimeTime(compatibility?.checked_at)],
          ]}
        />
        {compatibility?.mode === "managed" && (
          <DetailSection
            title="整批能力"
            values={[
              [
                "渠道上限",
                String(compatibility.capabilities.limits.max_channels),
              ],
              [
                "分组上限",
                String(compatibility.capabilities.limits.max_groups),
              ],
              [
                "模型上限",
                String(compatibility.capabilities.limits.max_models),
              ],
              ["重试次数", String(compatibility.capabilities.retry_times)],
              [
                "重试状态码",
                compatibility.capabilities.retry_status_codes.join("、") ||
                  "无",
              ],
              [
                "受管状态",
                compatibility.conflicts.length ? "存在冲突" : "可核对",
              ],
            ]}
          />
        )}
        <DetailSection
          title="线路与任务"
          values={[
            ["最近确认", formatRuntimeTime(site.route.confirmed_at)],
            [
              "线路版本",
              site.route.confirmed_version
                ? String(site.route.confirmed_version)
                : "尚无",
            ],
            [
              "最近同步",
              site.route.last_sync_status
                ? statusText(site.route.last_sync_status)
                : "尚无",
            ],
            ["待处理同步", String(site.route.pending_operations)],
            ["最近采集", formatRuntimeTime(site.collection.last_success_at)],
            ["最近评分", formatRuntimeTime(site.scoring.completed_at)],
            [
              "最近自动维护",
              formatRuntimeTime(site.automation.last_completed_at),
            ],
            ["最近产品数据", formatRuntimeTime(site.product.generated_at)],
          ]}
        />
      </div>
    </Modal>
  );
}

function DetailSection({
  title,
  values,
}: {
  title: string;
  values: [string, string][];
}) {
  return (
    <section className="detail-section">
      <h2>{title}</h2>
      <dl className="detail-list">
        {values.map(([label, value]) => (
          <div className="runtime-detail-pair" key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

const displayStatus: Record<string, string> = {
  normal: "正常",
  attention: "注意",
  blocked: "阻塞",
  fault: "故障",
  compatible: "兼容",
  incompatible: "不兼容",
  unverified: "待验证",
  unreachable: "无法连接",
  managed: "整批同步",
  legacy: "旧接口",
  succeeded: "成功",
  failed: "失败",
  uncertain: "结果待核对",
  retryable_failed: "等待重试",
  manual_required: "待人工处理",
};

function statusText(value: string): string {
  return displayStatus[value] ?? value;
}
