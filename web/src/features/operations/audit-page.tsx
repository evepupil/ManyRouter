import { useDeferredValue, useState } from "react";
import { Eye } from "lucide-react";
import { useList } from "../../api/hooks";
import type { AuditEvent } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  DateTime,
  IconButton,
  Modal,
  Select,
  Status,
} from "../../components/ui";
import { Page } from "../../components/page";
import { actionNames, objectNames } from "./labels";
import { useSession } from "../auth/auth";

export function AuditPage() {
  const { sites } = useScope();
  const session = useSession();
  const actorName = (event: AuditEvent) =>
    event.actor_type === "system"
      ? "系统"
      : event.actor_id === session.user.id
        ? session.user.username
        : "部署所有者";
  const [siteId, setSiteId] = useState("");
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [viewing, setViewing] = useState<AuditEvent | null>(null);
  const query = useList<AuditEvent>("audit", {
    site_id: siteId || undefined,
    q: useDeferredValue(search),
    offset,
  });
  return (
    <Page title="审计">
      <Toolbar
        search={search}
        onSearch={(value) => {
          setSearch(value);
          setOffset(0);
        }}
        onRefresh={() => {
          void query.refetch();
        }}
        placeholder="搜索动作、对象或原因"
      >
        <Select
          aria-label="审计站点"
          value={siteId}
          onChange={(event) => {
            setSiteId(event.target.value);
            setOffset(0);
          }}
        >
          <option value="">全部站点</option>
          {sites.map((site) => (
            <option key={site.id} value={site.id}>
              {site.name}
            </option>
          ))}
        </Select>
      </Toolbar>
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="暂无审计记录"
        columns={[
          {
            key: "date",
            title: "时间",
            render: (event) => <DateTime value={event.created_at} />,
          },
          {
            key: "actor",
            title: "操作者",
            render: (event) => actorName(event),
          },
          {
            key: "action",
            title: "操作",
            render: (event) => actionNames[event.action] ?? "配置变更",
          },
          {
            key: "site",
            title: "站点",
            render: (event) =>
              sites.find((site) => site.id === event.site_id)?.name ??
              (event.site_id ? event.site_id : "公共资料"),
          },
          {
            key: "reason",
            title: "原因",
            render: (event) => <span className="cell-sub">{event.reason}</span>,
          },
          {
            key: "result",
            title: "结果",
            render: (event) => <Status value={event.result} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (event) => (
              <div className="cell-actions">
                <IconButton
                  label="查看审计详情"
                  onClick={() => setViewing(event)}
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
        <Modal open onClose={() => setViewing(null)} title="审计详情">
          <dl className="detail-list">
            <dt>操作</dt>
            <dd>{actionNames[viewing.action] ?? "配置变更"}</dd>
            <dt>对象</dt>
            <dd>{objectNames[viewing.object_type] ?? "业务对象"}</dd>
            <dt>对象编号</dt>
            <dd>{viewing.object_id}</dd>
            <dt>操作者</dt>
            <dd>{actorName(viewing)}</dd>
            <dt>原因</dt>
            <dd>{viewing.reason}</dd>
            <dt>结果</dt>
            <dd>
              <Status value={viewing.result} />
            </dd>
            <dt>时间</dt>
            <dd>
              <DateTime value={viewing.created_at} />
            </dd>
          </dl>
        </Modal>
      )}
    </Page>
  );
}
