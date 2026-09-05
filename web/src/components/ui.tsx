import { Dialog as DialogPrimitive } from "@base-ui/react/dialog";
import { Tooltip } from "@base-ui/react/tooltip";
import {
  AlertCircle,
  CheckCircle2,
  Inbox,
  LoaderCircle,
  X,
} from "lucide-react";
import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
} from "react";
import { cloneElement, isValidElement, useId } from "react";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "danger" | "quiet";
  pending?: boolean;
  icon?: ReactNode;
};

export function Button({
  variant,
  pending,
  icon,
  children,
  className = "",
  disabled,
  ...props
}: ButtonProps) {
  return (
    <button
      type="button"
      className={`button ${variant ? `button-${variant}` : ""} ${className}`}
      disabled={disabled || pending}
      {...props}
    >
      {pending ? (
        <LoaderCircle aria-hidden="true" className="pending-icon" />
      ) : (
        icon
      )}
      {children}
    </button>
  );
}

export function IconButton({
  label,
  children,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger
        render={
          <button
            type="button"
            className="button button-quiet button-icon"
            aria-label={label}
            {...props}
          />
        }
      >
        {children}
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Positioner sideOffset={6}>
          <Tooltip.Popup className="tooltip">{label}</Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

export function Field({
  label,
  hint,
  children,
  full,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
  full?: boolean;
}) {
  const labelID = useId();
  const control = isValidElement<{ "aria-labelledby"?: string }>(children)
    ? cloneElement(children, { "aria-labelledby": labelID })
    : children;
  return (
    <label className={`field ${full ? "form-full" : ""}`}>
      <span id={labelID}>{label}</span>
      {control}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  );
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`input ${props.className ?? ""}`} />;
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`input ${props.className ?? ""}`} />;
}

export function Checkbox({
  label,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { label: ReactNode }) {
  return (
    <label className="check">
      <input type="checkbox" {...props} />
      <span>{label}</span>
    </label>
  );
}

export function Notice({
  error,
  success,
}: {
  error?: unknown;
  success?: string;
}) {
  if (error)
    return (
      <div className="notice notice-error" role="alert">
        <AlertCircle aria-hidden="true" />
        <span>
          {error instanceof Error ? error.message : "请求无法完成，请重试"}
        </span>
      </div>
    );
  if (success)
    return (
      <div className="notice notice-success" role="status">
        <CheckCircle2 aria-hidden="true" />
        <span>{success}</span>
      </div>
    );
  return null;
}

export function Empty({
  title,
  action,
}: {
  title: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <Inbox aria-hidden="true" />
      <span>{title}</span>
      {action}
    </div>
  );
}

export function Loading() {
  return (
    <div
      role="status"
      aria-live="polite"
      aria-busy="true"
      aria-label="正在读取"
    >
      <span className="sr-only">正在读取数据</span>
      <div className="skeleton" />
      <div className="skeleton" />
      <div className="skeleton" />
    </div>
  );
}

const statusNames: Record<string, string> = {
  enabled: "已启用",
  disabled: "已停用",
  active: "已生效",
  observing: "待上线",
  draft: "草案",
  pending: "待同步",
  syncing: "同步中",
  running: "执行中",
  applying: "发布中",
  compatible: "兼容",
  incompatible: "不兼容",
  unknown: "待检查",
  confirmed: "已确认",
  published: "已发布",
  applied: "已核对",
  succeeded: "成功",
  failed: "失败",
  uncertain: "结果待核对",
  retryable_failed: "等待重试",
  manual_required: "待人工处理",
  manual_locked: "人工停用",
  superseded: "已被替代",
  expired: "已过期",
};

export function Status({ value }: { value: string }) {
  const tone = [
    "enabled",
    "active",
    "compatible",
    "confirmed",
    "published",
    "applied",
    "succeeded",
  ].includes(value)
    ? "success"
    : ["failed", "incompatible"].includes(value)
      ? "danger"
      : ["syncing", "running", "applying"].includes(value)
        ? "info"
        : [
              "pending",
              "observing",
              "uncertain",
              "retryable_failed",
              "manual_required",
              "manual_locked",
            ].includes(value)
          ? "warning"
          : "";
  return (
    <span className={`badge ${tone ? `badge-${tone}` : ""}`}>
      {statusNames[value] ?? "待核对"}
    </span>
  );
}

export function Modal({
  open,
  onClose,
  title,
  children,
  wide,
  busy = false,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  wide?: boolean;
  busy?: boolean;
}) {
  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={(value, details) => {
        if (busy) {
          details.cancel();
          return;
        }
        if (!value) onClose();
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop className="dialog-backdrop" />
        <DialogPrimitive.Popup
          className={`dialog ${wide ? "dialog-wide" : ""}`}
          aria-busy={busy}
        >
          <div className="dialog-heading">
            <DialogPrimitive.Title className="dialog-title">
              {title}
            </DialogPrimitive.Title>
            <IconButton label="关闭" onClick={onClose} disabled={busy}>
              <X size={18} />
            </IconButton>
          </div>
          {children}
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export function DateTime({ value }: { value?: string | null }) {
  if (!value) return <span className="muted">未记录</span>;
  const date = new Date(value);
  return (
    <time className="nowrap" dateTime={value}>
      {Number.isNaN(date.getTime())
        ? value
        : date.toLocaleString("zh-CN", { hour12: false })}
    </time>
  );
}
