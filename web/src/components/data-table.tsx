import type { ReactNode } from "react";
import {
  ArrowLeftRight,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  Search,
} from "lucide-react";
import { Empty, IconButton, Input, Loading, Notice } from "./ui";

export interface Column<T> {
  key: string;
  title: string;
  render: (row: T) => ReactNode;
  className?: string;
}

export function DataTable<T extends { id: string }>({
  items,
  columns,
  loading,
  error,
  empty = "暂无记录",
}: {
  items: T[];
  columns: Column<T>[];
  loading?: boolean;
  error?: unknown;
  empty?: string;
}) {
  if (loading) return <Loading />;
  if (error) return <Notice error={error} />;
  return (
    <div className="table-container">
      <div
        className="table-scroll"
        role="region"
        aria-label="数据表"
        tabIndex={0}
      >
        <table className="table">
          <thead>
            <tr>
              {columns.map((column) => (
                <th
                  key={column.key}
                  className={`${column.className ?? ""} ${column.key === "actions" ? "table-actions" : ""}`}
                >
                  {column.title}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.id}>
                {columns.map((column) => (
                  <td
                    key={column.key}
                    className={`${column.className ?? ""} ${column.key === "actions" ? "table-actions" : ""}`}
                  >
                    {column.render(item)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {items.length === 0 && <Empty title={empty} />}
      </div>
      <div className="table-scroll-hint" aria-hidden="true">
        <ArrowLeftRight size={16} />
      </div>
    </div>
  );
}

export function Toolbar({
  search,
  onSearch,
  onRefresh,
  children,
  placeholder = "搜索名称或标识",
}: {
  search: string;
  onSearch: (value: string) => void;
  onRefresh: () => void;
  children?: ReactNode;
  placeholder?: string;
}) {
  return (
    <div className="toolbar">
      <label className="search-field">
        <Search aria-hidden="true" />
        <Input
          aria-label={placeholder}
          placeholder={placeholder}
          value={search}
          onChange={(event) => onSearch(event.target.value)}
        />
      </label>
      <div className="toolbar-group">
        {children}
        <IconButton label="刷新" onClick={onRefresh}>
          <RefreshCw size={17} />
        </IconButton>
      </div>
    </div>
  );
}

export function Pagination({
  total,
  offset,
  limit = 20,
  onChange,
}: {
  total: number;
  offset: number;
  limit?: number;
  onChange: (offset: number) => void;
}) {
  return (
    <div className="pagination">
      <span>
        {total > 0
          ? `${offset + 1}–${Math.min(offset + limit, total)} / ${total}`
          : "0 条记录"}
      </span>
      <IconButton
        label="上一页"
        disabled={offset === 0}
        onClick={() => onChange(Math.max(0, offset - limit))}
      >
        <ChevronLeft size={17} />
      </IconButton>
      <IconButton
        label="下一页"
        disabled={offset + limit >= total}
        onClick={() => onChange(offset + limit)}
      >
        <ChevronRight size={17} />
      </IconButton>
    </div>
  );
}
