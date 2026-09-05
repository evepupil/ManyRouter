import type { BadgeTone } from "../../components/ui";
import type {
  EvaluationPurpose,
  EvaluationRun,
  ShadowRecommendation,
  TrustedReferenceInput,
} from "../../api/types";

export interface ToneLabel {
  label: string;
  tone?: BadgeTone;
}

export const purposeNames: Record<EvaluationPurpose, string> = {
  health: "健康检查",
  authenticity: "真实性",
  quality: "能力质量",
  recovery: "恢复确认",
};

export const trustNames: Record<TrustedReferenceInput["trust"], string> = {
  official: "官方来源",
  operator_trusted: "运营确认",
  community: "社区参考（仅作对照）",
};

export function confidenceLabel(value: string): ToneLabel {
  switch (value) {
    case "high":
      return { label: "高", tone: "success" };
    case "medium":
      return { label: "中", tone: "info" };
    case "low":
      return { label: "低", tone: "warning" };
    default:
      return { label: "证据不足", tone: "warning" };
  }
}

export function eligibilityLabel(value: string): ToneLabel {
  switch (value) {
    case "eligible":
      return { label: "可参与", tone: "success" };
    case "excluded":
      return { label: "建议排除", tone: "danger" };
    default:
      return { label: "继续观察", tone: "warning" };
  }
}

export function authenticityLabel(value: string): ToneLabel {
  switch (value) {
    case "consistent":
      return { label: "一致", tone: "success" };
    case "suspicious":
      return { label: "待复测", tone: "warning" };
    case "inconsistent":
      return { label: "不一致", tone: "danger" };
    default:
      return { label: "证据不足" };
  }
}

export function evaluationStatusLabel(
  value: EvaluationRun["status"],
): ToneLabel {
  switch (value) {
    case "succeeded":
      return { label: "成功", tone: "success" };
    case "failed":
      return { label: "失败", tone: "danger" };
    case "running":
      return { label: "执行中", tone: "info" };
    case "pending":
      return { label: "等待执行", tone: "warning" };
    case "uncertain":
      return { label: "结果待核对", tone: "warning" };
    case "cancelled":
      return { label: "已取消" };
  }
}

export function recommendationLabel(
  value: ShadowRecommendation["action"],
): ToneLabel {
  switch (value) {
    case "join":
      return { label: "建议加入", tone: "success" };
    case "keep":
      return { label: "建议保留", tone: "info" };
    case "exit":
      return { label: "建议退出", tone: "danger" };
    case "exclude":
      return { label: "建议排除", tone: "danger" };
    case "watch":
      return { label: "继续观察", tone: "warning" };
  }
}

const strategyNames: Record<ShadowRecommendation["strategy_kind"], string> = {
  lowest_price: "最低价",
  low_latency: "低延迟",
  high_sla: "高 SLA",
  high_quality: "高质量",
  balanced: "均衡",
};

export function strategyName(
  value: ShadowRecommendation["strategy_kind"],
): string {
  return strategyNames[value];
}

const reasonNames: Record<string, string> = {
  hard_gate_failed: "命中硬性排除条件",
  hard_gate_pending: "硬性条件仍待确认",
  evidence_not_ready: "当前证据不足",
  join_threshold_met: "达到准入线并完成连续确认",
  join_confirmation_pending: "达到准入线，仍待连续确认",
  member_within_exit_line: "现有成员仍高于退出线",
  exit_threshold_met: "低于退出线并完成连续确认",
  exit_confirmation_pending: "低于退出线，仍待连续确认",
  below_join_threshold: "尚未达到准入线",
  authenticity_mismatch: "模型真实性不一致",
  credential_invalid: "供应商凭证无效",
  balance_unavailable: "供应商余额不可用",
  consecutive_failures: "连续供应商故障达到门槛",
  major_risk: "重大风险仍未解除",
};

export function reasonName(value: string): string {
  return reasonNames[value] ?? value;
}

export function sourceName(value: string): string {
  switch (value) {
    case "":
      return "尚未建立采集位置";
    case "new_api_http":
      return "New API 接口";
    case "new_api_view":
      return "New API 视图";
    default:
      return value;
  }
}
