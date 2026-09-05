import { useState, type FormEvent } from "react";
import { Pencil, PowerOff, Save } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type { Relation, Strategy, StrategyKind } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable } from "../../components/data-table";
import {
  Button,
  Checkbox,
  Empty,
  Field,
  IconButton,
  Input,
  Loading,
  Modal,
  Notice,
  Status,
} from "../../components/ui";
import { Page, SiteRequired } from "../../components/page";
import { EntryAccess } from "../../components/entry-access";

const kinds: { kind: StrategyKind; name: string }[] = [
  { kind: "lowest_price", name: "最低价格" },
  { kind: "low_latency", name: "低延迟" },
  { kind: "high_sla", name: "高可用" },
  { kind: "high_quality", name: "高质量" },
  { kind: "balanced", name: "均衡" },
];

interface StrategyRow {
  id: string;
  kind: StrategyKind;
  name: string;
  strategy?: Strategy;
}

export function AutoPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `人工 Auto · ${site.name}` : "人工 Auto"}>
      <SiteRequired>
        <AutoList key={siteId} siteId={siteId} />
      </SiteRequired>
    </Page>
  );
}

function AutoList({ siteId }: { siteId: string }) {
  const query = useList<Strategy>("strategies", {
    site_id: siteId,
    limit: 100,
  });
  const [editing, setEditing] = useState<StrategyRow | null>(null);
  const rows: StrategyRow[] = kinds.map(({ kind, name }) => ({
    id: kind,
    kind,
    name,
    strategy: query.data?.items.find((item) => item.kind === kind),
  }));
  return (
    <>
      <DataTable
        items={rows}
        loading={query.isPending}
        error={query.error}
        columns={[
          {
            key: "name",
            title: "Auto",
            render: (row) => (
              <>
                <div className="cell-title">
                  {row.strategy?.display_name ?? row.name}
                </div>
                <div className="cell-sub">{row.name}</div>
              </>
            ),
          },
          {
            key: "members",
            title: "成员",
            className: "numeric",
            render: (row) => row.strategy?.member_relation_ids?.length ?? 0,
          },
          {
            key: "visible",
            title: "用户入口",
            render: (row) =>
              row.strategy
                ? row.strategy.visible
                  ? "用户入口开放"
                  : "用户入口关闭"
                : "未配置",
          },
          {
            key: "price",
            title: "销售倍率",
            className: "numeric",
            render: (row) => row.strategy?.sale_ratio ?? "未定价",
          },
          {
            key: "status",
            title: "状态",
            render: (row) =>
              row.strategy ? (
                <Status value={row.strategy.enabled ? "enabled" : "disabled"} />
              ) : (
                <span className="badge">未配置</span>
              ),
          },
          {
            key: "actions",
            title: "操作",
            render: (row) => (
              <div className="cell-actions">
                <IconButton
                  label={`配置 ${row.name}`}
                  onClick={() => setEditing(row)}
                >
                  <Pencil size={16} />
                </IconButton>
              </div>
            ),
          },
        ]}
      />
      {editing && (
        <StrategyEditor
          siteId={siteId}
          row={editing}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}

function StrategyEditor({
  siteId,
  row,
  onClose,
}: {
  siteId: string;
  row: StrategyRow;
  onClose: () => void;
}) {
  const [name, setName] = useState(row.strategy?.display_name ?? row.name);
  const [enabled, setEnabled] = useState(row.strategy?.enabled ?? false);
  const [visible, setVisible] = useState(row.strategy?.visible ?? true);
  const [members, setMembers] = useState<string[]>(
    row.strategy?.member_relation_ids ?? [],
  );
  const [reason, setReason] = useState("");
  const relations = useList<Relation>("relations", {
    site_id: siteId,
    limit: 100,
  });
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `sites/${siteId}/strategies/${row.kind}`,
      method: "PUT",
      body: {
        version: row.strategy?.version ?? 0,
        enabled,
        visible,
        display_name: name,
        member_relation_ids: members,
        reason,
      },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`配置 Auto · ${row.name}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <Field label="展示名称">
          <Input
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <div className="toolbar-group">
          <Checkbox
            label="启用 Auto"
            checked={enabled}
            onChange={(event) => setEnabled(event.target.checked)}
          />
        </div>
        <EntryAccess open={visible} onChange={setVisible} auto />
        <h2>供应商成员</h2>
        <Notice error={relations.error} />
        {relations.error && (
          <Button
            onClick={() => {
              void relations.refetch();
            }}
            pending={relations.isFetching}
          >
            重新读取供应商成员
          </Button>
        )}
        <div className="selection-list">
          {relations.isPending && <Loading />}
          {!relations.isPending &&
            !relations.isError &&
            relations.data?.items.map((relation) => (
              <div className="selection-item" key={relation.id}>
                <Checkbox
                  label={relation.supplier_name ?? relation.group_display_name}
                  checked={members.includes(relation.id)}
                  onChange={(event) =>
                    setMembers((value) =>
                      event.target.checked
                        ? [...value, relation.id]
                        : value.filter((id) => id !== relation.id),
                    )
                  }
                />
                <Status value={relation.desired_status} />
              </div>
            ))}
          {relations.isSuccess && relations.data.items.length === 0 && (
            <Empty title="当前站点尚未投放供应商" />
          )}
        </div>
        <Field label="修改原因">
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
            variant={!enabled || !visible ? "danger" : "primary"}
            icon={!enabled ? <PowerOff /> : <Save />}
            pending={action.isPending}
            disabled={relations.isPending || !!relations.error}
          >
            {!enabled
              ? "保存并停用 Auto"
              : visible
                ? "保存 Auto"
                : "保存并关闭用户入口"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
