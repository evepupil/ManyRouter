import { TriangleAlert } from "lucide-react";
import { Checkbox } from "./ui";

export function EntryAccess({
  open,
  onChange,
  auto = false,
  full = false,
}: {
  open: boolean;
  onChange: (value: boolean) => void;
  auto?: boolean;
  full?: boolean;
}) {
  const label = auto
    ? open
      ? "用户入口开放"
      : "用户入口关闭"
    : open
      ? "入口开放"
      : "入口关闭";
  return (
    <div className={`form-stack ${full ? "form-full" : ""}`}>
      <Checkbox
        label={label}
        checked={open}
        onChange={(event) => onChange(event.target.checked)}
      />
      {!open && (
        <div className="notice notice-warning" role="note">
          <TriangleAlert aria-hidden="true" />
          <span>关闭后，该组已有密钥也无法调用。</span>
        </div>
      )}
    </div>
  );
}
