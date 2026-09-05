import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Play, RefreshCw } from "lucide-react";
import { queryString, request } from "../../api/client";
import { useAction } from "../../api/hooks";
import type { CollectionStatus, CollectionStatusList } from "../../api/types";
import { DataTable } from "../../components/data-table";
import {
  Badge,
  Button,
  DateTime,
  IconButton,
  Notice,
} from "../../components/ui";
import { sourceName } from "./labels";

export function CollectionView({
  siteId,
  siteEnabled,
}: {
  siteId: string;
  siteEnabled: boolean;
}) {
  const [success, setSuccess] = useState("");
  const query = useQuery({
    queryKey: ["ops", "collection-status", { siteId }],
    queryFn: ({ signal }) =>
      request<CollectionStatusList>(
        `/ops/collection-status${queryString({ site_id: siteId })}`,
        { signal },
      ),
  });
  const collect = useAction(() => {
    setSuccess("采集已完成");
  });
  const rows = (query.data?.items ?? []).map((item) => ({
    ...item,
    id: `${item.site_id}:${item.source_kind}`,
  }));

  return (
    <div className="form-stack observability-collection">
      <div className="toolbar toolbar-actions-only">
        <div className="toolbar-group">
          {!siteEnabled && <Badge tone="warning">站点已停用</Badge>}
          <Button
            variant="primary"
            icon={<Play />}
            pending={collect.isPending}
            disabled={!siteEnabled}
            onClick={() => {
              setSuccess("");
              collect.reset();
              collect.mutate(
                {
                  path: "collection-runs",
                  body: { site_id: siteId },
                },
                {
                  onSettled: () => {
                    void query.refetch();
                  },
                },
              );
            }}
          >
            立即采集
          </Button>
          <IconButton
            label="刷新采集状态"
            disabled={query.isFetching}
            onClick={() => {
              void query.refetch();
            }}
          >
            <RefreshCw size={17} />
          </IconButton>
        </div>
      </div>
      <Notice error={collect.error} success={success} />
      <DataTable
        items={rows}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无采集记录"
        columns={[
          {
            key: "source",
            title: "数据来源",
            render: (status) => (
              <>
                <div className="cell-title">
                  {sourceName(status.source_kind)}
                </div>
                <div className="cell-sub">
                  {status.contract_version || "未记录协议版本"}
                </div>
              </>
            ),
          },
          {
            key: "state",
            title: "采集状态",
            render: (status) => <CollectionState status={status} />,
          },
          {
            key: "cursor",
            title: "最近事件",
            render: (status) => <DateTime value={status.cursor_time} />,
          },
          {
            key: "scanned-through",
            title: "扫描至",
            render: (status) => <DateTime value={status.scanned_through} />,
          },
          {
            key: "source-latest",
            title: "源端最新",
            render: (status) => <DateTime value={status.source_latest} />,
          },
          {
            key: "success",
            title: "最近成功",
            render: (status) => <DateTime value={status.last_success_at} />,
          },
          {
            key: "read",
            title: "最近读取",
            render: (status) => <DateTime value={status.last_read_at} />,
          },
          {
            key: "updated",
            title: "状态更新",
            render: (status) => <DateTime value={status.updated_at} />,
          },
        ]}
      />
    </div>
  );
}

function CollectionState({ status }: { status: CollectionStatus }) {
  if (status.data_gap)
    return (
      <div className="state-with-detail">
        <Badge tone="danger">存在数据缺口</Badge>
        <CollectionError status={status} />
      </div>
    );
  if (status.consecutive_failures > 0)
    return (
      <div className="state-with-detail">
        <Badge tone="warning">
          连续失败 {status.consecutive_failures.toLocaleString("zh-CN")} 次
        </Badge>
        <CollectionError status={status} />
      </div>
    );
  if (status.last_success_at)
    return <CollectionFreshness value={status.last_success_at} />;
  return <Badge>尚未采集</Badge>;
}

function CollectionFreshness({ value }: { value: string }) {
  const timestamp = new Date(value).getTime();
  if (Number.isNaN(timestamp)) return <Badge>有成功记录</Badge>;
  const age = Date.now() - timestamp;
  if (age <= 15 * 60 * 1000) return <Badge tone="success">采集正常</Badge>;
  if (age <= 60 * 60 * 1000) return <Badge tone="info">采集延迟</Badge>;
  return <Badge tone="warning">采集记录已过期</Badge>;
}

function CollectionError({ status }: { status: CollectionStatus }) {
  if (!status.last_error_message && !status.last_error_code) return null;
  return (
    <span className="cell-sub">
      {status.last_error_message || status.last_error_code}
    </span>
  );
}
