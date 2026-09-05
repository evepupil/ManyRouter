import { useDeferredValue, useState, type FormEvent } from "react";
import { Plus, Send } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type { Price, Relation, Strategy } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Button,
  DateTime,
  Field,
  Input,
  Modal,
  Notice,
  Select,
  Status,
} from "../../components/ui";
import { Page, SiteRequired } from "../../components/page";

export function PricingPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `售价 · ${site.name}` : "售价"}>
      <SiteRequired>
        <PricingList key={siteId} siteId={siteId} />
      </SiteRequired>
    </Page>
  );
}

function PricingList({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [creating, setCreating] = useState(false);
  const [publishing, setPublishing] = useState<Price | null>(null);
  const query = useList<Price>("prices", {
    site_id: siteId,
    q: useDeferredValue(search),
    offset,
  });
  const relations = useList<Relation>("relations", {
    site_id: siteId,
    limit: 100,
  });
  const strategies = useList<Strategy>("strategies", {
    site_id: siteId,
    limit: 100,
  });
  const groups = [
    ...(relations.data?.items ?? []).map((item) => ({
      key: item.group_key,
      name: item.group_display_name,
    })),
    ...(strategies.data?.items ?? []).map((item) => ({
      key: item.group_key,
      name: item.display_name,
    })),
  ];
  return (
    <>
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
          icon={<Plus />}
          onClick={() => setCreating(true)}
        >
          新增售价草案
        </Button>
      </Toolbar>
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="当前站点暂无售价记录"
        columns={[
          {
            key: "group",
            title: "分组",
            render: (price) => (
              <div className="cell-title">
                {groups.find((group) => group.key === price.group_key)?.name ??
                  price.group_key}
              </div>
            ),
          },
          {
            key: "ratio",
            title: "销售倍率",
            className: "numeric",
            render: (price) => price.sale_ratio,
          },
          {
            key: "version",
            title: "版本",
            className: "numeric",
            render: (price) => price.version,
          },
          {
            key: "status",
            title: "状态",
            render: (price) => <Status value={price.status} />,
          },
          {
            key: "confirmed",
            title: "核对记录",
            render: (price) => (
              <>
                {price.is_last_confirmed && (
                  <span className="badge badge-info">最近确认</span>
                )}
                <div className="cell-sub">
                  {price.applied_at ? (
                    <DateTime value={price.applied_at} />
                  ) : (
                    "尚未确认"
                  )}
                </div>
              </>
            ),
          },
          { key: "reason", title: "修改原因", render: (price) => price.reason },
          {
            key: "date",
            title: "创建时间",
            render: (price) => <DateTime value={price.created_at} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (price) =>
              price.status === "draft" ? (
                <Button icon={<Send />} onClick={() => setPublishing(price)}>
                  发布
                </Button>
              ) : null,
          },
        ]}
      />
      <Pagination
        total={query.data?.total ?? 0}
        offset={offset}
        onChange={setOffset}
      />
      {creating && (
        <PriceEditor
          siteId={siteId}
          groups={groups}
          loading={relations.isPending || strategies.isPending}
          error={relations.error ?? strategies.error}
          onClose={() => setCreating(false)}
        />
      )}
      {publishing && (
        <PublishPrice
          price={publishing}
          groupName={
            groups.find((group) => group.key === publishing.group_key)?.name ??
            publishing.group_key
          }
          onClose={() => setPublishing(null)}
        />
      )}
    </>
  );
}

function PriceEditor({
  siteId,
  groups,
  loading,
  error,
  onClose,
}: {
  siteId: string;
  groups: { key: string; name: string }[];
  loading: boolean;
  error: unknown;
  onClose: () => void;
}) {
  const [groupKey, setGroupKey] = useState("");
  const [ratio, setRatio] = useState("");
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: "prices",
      body: { site_id: siteId, group_key: groupKey, sale_ratio: ratio, reason },
    });
  };
  return (
    <Modal open busy={action.isPending} onClose={onClose} title="新增售价草案">
      <form className="form-stack" onSubmit={submit}>
        <Field label="分组">
          <Select
            required
            value={groupKey}
            onChange={(event) => setGroupKey(event.target.value)}
          >
            <option value="">选择分组</option>
            {groups.map((group) => (
              <option key={group.key} value={group.key}>
                {group.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="销售倍率">
          <Input
            required
            inputMode="decimal"
            pattern="[0-9]+(\.[0-9]{1,6})?"
            value={ratio}
            onChange={(event) => setRatio(event.target.value)}
          />
        </Field>
        <Field label="修改原因">
          <Input
            required
            maxLength={500}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
        <Notice error={action.error ?? error} />
        <div className="form-actions">
          <Button onClick={onClose} disabled={action.isPending}>
            取消
          </Button>
          <Button
            type="submit"
            variant="primary"
            pending={action.isPending}
            disabled={loading || !!error}
          >
            保存草案
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function PublishPrice({
  price,
  groupName,
  onClose,
}: {
  price: Price;
  groupName: string;
  onClose: () => void;
}) {
  const { site } = useScope();
  const action = useAction(onClose);
  return (
    <Modal open busy={action.isPending} onClose={onClose} title="发布售价">
      <div className="form-stack">
        <dl className="detail-list">
          <dt>站点</dt>
          <dd>{site?.name}</dd>
          <dt>分组</dt>
          <dd>{groupName}</dd>
          <dt>销售倍率</dt>
          <dd>{price.sale_ratio}</dd>
          <dt>版本</dt>
          <dd>{price.version}</dd>
        </dl>
        <Notice error={action.error} />
        <div className="form-actions">
          <Button onClick={onClose} disabled={action.isPending}>
            取消
          </Button>
          <Button
            variant="primary"
            icon={<Send />}
            pending={action.isPending}
            onClick={() =>
              action.mutate({
                path: `prices/${price.id}/publish`,
                body: { version: price.version },
              })
            }
          >
            确认发布
          </Button>
        </div>
      </div>
    </Modal>
  );
}
