import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { History, Info, RefreshCw, ScanSearch } from "lucide-react";
import { request } from "../../api/client";
import { useAction } from "../../api/hooks";
import type { RuntimeHealth, RuntimeSite } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable } from "../../components/data-table";
import {
  Badge,
  IconButton,
  Loading,
  Notice,
  Status,
} from "../../components/ui";
import { Page } from "../../components/page";
import { RuntimeDetail } from "./runtime-health-detail";
import { FactCell, SystemOverview } from "./runtime-health-summary";

type RuntimeRow = RuntimeSite & { id: string };

export function RuntimeHealthPage() {
  const { siteId, setSiteId } = useScope();
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: ["ops", "runtime-health"],
    queryFn: ({ signal }) =>
      request<RuntimeHealth>("/ops/runtime-health", { signal }),
    refetchInterval: 30_000,
  });
  const check = useAction<RuntimeSite>();
  const [checkingSite, setCheckingSite] = useState("");
  const [detailSiteId, setDetailSiteId] = useState("");
  const rows: RuntimeRow[] = (query.data?.sites ?? []).map((item) => ({
    ...item,
    id: item.site_id,
  }));
  const detail = rows.find((row) => row.site_id === detailSiteId) ?? null;
  const checkSite = (row: RuntimeRow) => {
    setCheckingSite(row.site_id);
    check.mutate(
      { path: `runtime-health/${row.site_id}/check`, body: {} },
      { onSettled: () => setCheckingSite("") },
    );
  };

  return (
    <Page
      title="运行状态"
      action={
        <IconButton
          label="刷新运行状态"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw
            size={17}
            className={query.isFetching ? "pending-icon" : undefined}
          />
        </IconButton>
      }
    >
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <Notice error={query.error} />
      ) : query.data ? (
        <>
          <SystemOverview data={query.data} />
          <Notice error={check.error} />
          <div className="runtime-health-table">
            <DataTable
              items={rows}
              empty="尚未接入站点"
              columns={[
                {
                  key: "site",
                  title: "站点",
                  render: (row) => (
                    <>
                      <div className="inline-heading">
                        <span className="cell-title">{row.site_name}</span>
                        {row.site_id === siteId && <Badge>当前站点</Badge>}
                      </div>
                      <div className="cell-sub">{row.site_code}</div>
                      {row.reasons[0] && (
                        <div className="cell-sub runtime-site-issue">
                          {row.reasons[0].message}
                          {row.reasons.length > 1 &&
                            `，另有 ${row.reasons.length - 1} 项`}
                        </div>
                      )}
                    </>
                  ),
                },
                {
                  key: "status",
                  title: "状态",
                  render: (row) => <Status value={row.status} />,
                },
                {
                  key: "compatibility",
                  title: "兼容",
                  render: (row) =>
                    row.compatibility ? (
                      <div className="state-with-detail">
                        <div className="inline-badges">
                          <Status value={row.compatibility.verdict} />
                          <Status value={row.compatibility.mode} />
                        </div>
                        <span className="cell-sub">
                          {row.compatibility.new_api_version || "版本待核对"}
                        </span>
                      </div>
                    ) : (
                      <Status value="unknown" />
                    ),
                },
                {
                  key: "route",
                  title: "线路",
                  render: (row) => (
                    <FactCell
                      value={row.route.last_sync_status}
                      time={row.route.confirmed_at}
                      empty="尚未确认"
                    />
                  ),
                },
                {
                  key: "collection",
                  title: "采集",
                  render: (row) => (
                    <FactCell
                      value={
                        row.collection.data_gap
                          ? "存在缺口"
                          : row.collection.consecutive_failures > 0
                            ? `连续失败 ${row.collection.consecutive_failures} 次`
                            : ""
                      }
                      time={row.collection.last_success_at}
                      empty="尚未采集"
                    />
                  ),
                },
                {
                  key: "scoring",
                  title: "评分 / 自动维护",
                  render: (row) => (
                    <FactCell
                      value={
                        row.automation.automatic_strategies > 0
                          ? `${row.automation.automatic_strategies} 项自动`
                          : row.scoring.last_status || "人工维护"
                      }
                      time={
                        row.automation.last_completed_at ??
                        row.scoring.completed_at
                      }
                      empty="尚无评分"
                    />
                  ),
                },
                {
                  key: "product",
                  title: "产品数据",
                  render: (row) => (
                    <FactCell
                      value={
                        row.product.version
                          ? `第 ${row.product.version} 版`
                          : ""
                      }
                      time={row.product.generated_at}
                      empty="尚未生成"
                    />
                  ),
                },
                {
                  key: "actions",
                  title: "操作",
                  render: (row) => (
                    <div className="cell-actions">
                      <IconButton
                        label={`重新检查 ${row.site_name}`}
                        onClick={() => checkSite(row)}
                        disabled={check.isPending}
                      >
                        {checkingSite === row.site_id ? (
                          <RefreshCw size={16} className="pending-icon" />
                        ) : (
                          <ScanSearch size={16} />
                        )}
                      </IconButton>
                      <IconButton
                        label={`查看 ${row.site_name} 运行详情`}
                        onClick={() => setDetailSiteId(row.site_id)}
                      >
                        <Info size={16} />
                      </IconButton>
                      <IconButton
                        label={`查看 ${row.site_name} 同步记录`}
                        onClick={() => {
                          setSiteId(row.site_id);
                          void navigate({ to: "/operations" });
                        }}
                      >
                        <History size={16} />
                      </IconButton>
                    </div>
                  ),
                },
              ]}
            />
          </div>
          {detail && (
            <RuntimeDetail site={detail} onClose={() => setDetailSiteId("")} />
          )}
        </>
      ) : null}
    </Page>
  );
}
