import { useDeferredValue, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Eye, RotateCcw, Send } from "lucide-react";
import { request } from "../../api/client";
import { useAction, useList } from "../../api/hooks";
import type { Plan } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Button,
  DateTime,
  Field,
  IconButton,
  Input,
  Loading,
  Modal,
  Notice,
  Status,
} from "../../components/ui";
import { Page, SiteRequired } from "../../components/page";
import { PlanDiff } from "./plan-diff";

export function PlansPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `线路版本 · ${site.name}` : "线路版本"}>
      <SiteRequired>
        <PlanList key={siteId} siteId={siteId} />
      </SiteRequired>
    </Page>
  );
}

function PlanList({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [viewing, setViewing] = useState<Plan | null>(null);
  const [restoring, setRestoring] = useState<Plan | null>(null);
  const [submitted, setSubmitted] = useState(false);
  const query = useList<Plan>("plans", {
    site_id: siteId,
    q: useDeferredValue(search),
    offset,
  });
  const sync = useAction(() => setSubmitted(true));
  return (
    <>
      <Notice
        error={sync.error}
        success={submitted ? "已提交同步" : undefined}
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
          variant="primary"
          icon={<Send />}
          pending={sync.isPending}
          onClick={() => {
            setSubmitted(false);
            sync.mutate({ path: `sites/${siteId}/sync` });
          }}
        >
          同步当前线路
        </Button>
      </Toolbar>
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无线路版本"
        columns={[
          {
            key: "version",
            title: "版本",
            className: "numeric",
            render: (plan) => plan.version,
          },
          {
            key: "reason",
            title: "修改原因",
            render: (plan) => <div className="cell-title">{plan.reason}</div>,
          },
          {
            key: "status",
            title: "状态",
            render: (plan) => <Status value={plan.status} />,
          },
          {
            key: "date",
            title: "创建时间",
            render: (plan) => <DateTime value={plan.created_at} />,
          },
          {
            key: "confirmed",
            title: "确认时间",
            render: (plan) => <DateTime value={plan.confirmed_at} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (plan) => (
              <div className="cell-actions">
                <IconButton
                  label={`查看版本 ${plan.version}`}
                  onClick={() => setViewing(plan)}
                >
                  <Eye size={16} />
                </IconButton>
                <IconButton
                  label={`恢复版本 ${plan.version}`}
                  onClick={() => setRestoring(plan)}
                >
                  <RotateCcw size={16} />
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
        <PlanDetail plan={viewing} onClose={() => setViewing(null)} />
      )}
      {restoring && (
        <RestorePlan
          plan={restoring}
          onClose={() => setRestoring(null)}
          onSaved={() => {
            setRestoring(null);
            setSubmitted(true);
          }}
        />
      )}
    </>
  );
}

function PlanDetail({ plan, onClose }: { plan: Plan; onClose: () => void }) {
  const query = useQuery({
    queryKey: ["ops", "plan", plan.id],
    queryFn: ({ signal }) => request<Plan>(`/ops/plans/${plan.id}`, { signal }),
  });
  const data = query.data;
  return (
    <Modal open onClose={onClose} title={`线路版本 ${plan.version}`} wide>
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <Notice error={query.error} />
      ) : (
        <div className="form-stack">
          <dl className="detail-list">
            <dt>版本</dt>
            <dd>{data?.version}</dd>
            <dt>状态</dt>
            <dd>
              <Status value={data?.status ?? plan.status} />
            </dd>
            <dt>修改原因</dt>
            <dd>{data?.reason}</dd>
            <dt>创建时间</dt>
            <dd>
              <DateTime value={data?.created_at} />
            </dd>
          </dl>
          <PlanDiff
            previous={data?.previous_snapshot}
            current={data?.snapshot}
          />
        </div>
      )}
    </Modal>
  );
}

function RestorePlan({
  plan,
  onClose,
  onSaved,
}: {
  plan: Plan;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { site } = useScope();
  const [reason, setReason] = useState("");
  const action = useAction(onSaved);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({ path: `plans/${plan.id}/restore`, body: { reason } });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`恢复线路版本 ${plan.version}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <dl className="detail-list">
          <dt>站点</dt>
          <dd>{site?.name}</dd>
          <dt>目标版本</dt>
          <dd>{plan.version}</dd>
          <dt>原修改原因</dt>
          <dd>{plan.reason}</dd>
        </dl>
        <Field label="恢复原因">
          <Input
            required
            maxLength={500}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
        <Notice error={action.error} />
        <div className="form-actions">
          <Button onClick={onClose} disabled={action.isPending}>
            取消
          </Button>
          <Button
            type="submit"
            variant="danger"
            icon={<RotateCcw />}
            pending={action.isPending}
          >
            确认恢复
          </Button>
        </div>
      </form>
    </Modal>
  );
}
