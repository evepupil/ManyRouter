import { useDeferredValue, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Eye, RefreshCw } from "lucide-react";
import { request } from "../../api/client";
import { useAction, useList } from "../../api/hooks";
import type { SyncOperation } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Button,
  DateTime,
  IconButton,
  Loading,
  Modal,
  Notice,
  Status,
} from "../../components/ui";
import { Page, SiteRequired } from "../../components/page";
import { stepLabel } from "./labels";

export function OperationsPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `同步操作 · ${site.name}` : "同步操作"}>
      <SiteRequired>
        <OperationList key={siteId} siteId={siteId} />
      </SiteRequired>
    </Page>
  );
}

function OperationList({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [viewing, setViewing] = useState<SyncOperation | null>(null);
  const query = useList<SyncOperation>(
    "sync-operations",
    { site_id: siteId, q: useDeferredValue(search), offset },
    { poll: true },
  );
  const action = useAction();
  return (
    <>
      <Notice
        error={action.error}
        success={action.isSuccess ? "已提交同步" : undefined}
      />
      <Toolbar
        search={search}
        onSearch={(value) => {
          setSearch(value);
          setOffset(0);
        }}
        onRefresh={() => {
          void query.refetch();
        }}
      >
        <Button
          icon={<RefreshCw />}
          pending={action.isPending}
          onClick={() => action.mutate({ path: `sites/${siteId}/sync` })}
        >
          重新同步
        </Button>
      </Toolbar>
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无同步记录"
        columns={[
          {
            key: "date",
            title: "提交时间",
            render: (operation) => <DateTime value={operation.created_at} />,
          },
          {
            key: "status",
            title: "状态",
            render: (operation) => <Status value={operation.status} />,
          },
          {
            key: "step",
            title: "当前步骤",
            render: (operation) => stepLabel(operation.current_step),
          },
          {
            key: "attempt",
            title: "尝试次数",
            className: "numeric",
            render: (operation) => operation.attempt,
          },
          {
            key: "error",
            title: "处理结果",
            render: (operation) => (
              <span className="cell-sub">
                {operation.last_error_message ??
                  (operation.status === "succeeded" ? "配置已确认" : "")}
              </span>
            ),
          },
          {
            key: "actions",
            title: "操作",
            render: (operation) => (
              <div className="cell-actions">
                <IconButton
                  label="查看同步详情"
                  onClick={() => setViewing(operation)}
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
        <OperationDetail operation={viewing} onClose={() => setViewing(null)} />
      )}
    </>
  );
}

function OperationDetail({
  operation,
  onClose,
}: {
  operation: SyncOperation;
  onClose: () => void;
}) {
  const query = useQuery({
    queryKey: ["ops", "sync-operation", operation.id],
    queryFn: ({ signal }) =>
      request<SyncOperation>(`/ops/sync-operations/${operation.id}`, {
        signal,
      }),
    refetchInterval: (state) =>
      ["pending", "running", "retryable_failed", "uncertain"].includes(
        state.state.data?.status ?? "",
      )
        ? 5000
        : false,
  });
  return (
    <Modal open onClose={onClose} title="同步详情" wide>
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <Notice error={query.error} />
      ) : (
        <div className="form-stack">
          <dl className="detail-list">
            <dt>状态</dt>
            <dd>
              <Status value={query.data?.status ?? operation.status} />
            </dd>
            <dt>提交时间</dt>
            <dd>
              <DateTime value={query.data?.created_at} />
            </dd>
            <dt>完成时间</dt>
            <dd>
              <DateTime value={query.data?.completed_at} />
            </dd>
          </dl>
          {query.data?.last_error_message && (
            <Notice error={new Error(query.data.last_error_message)} />
          )}
          <DataTable
            items={(query.data?.steps ?? []).map((step) => ({
              ...step,
              id: step.step_key,
            }))}
            empty="尚未开始执行"
            columns={[
              {
                key: "step",
                title: "执行步骤",
                render: (step) => stepLabel(step.step_key),
              },
              {
                key: "status",
                title: "状态",
                render: (step) => <Status value={step.status} />,
              },
              {
                key: "error",
                title: "结果",
                render: (step) => step.error_message ?? "",
              },
              {
                key: "date",
                title: "完成时间",
                render: (step) => <DateTime value={step.finished_at} />,
              },
            ]}
          />
        </div>
      )}
    </Modal>
  );
}
