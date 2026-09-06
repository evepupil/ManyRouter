import { useState, type FormEvent } from "react";
import { Bot, Pencil, Play, PowerOff, Save } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type {
  AutomationRun,
  AutomationSetting,
  Relation,
  Strategy,
  StrategyKind,
} from "../../api/types";
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
  automation?: AutomationSetting;
}

export function AutoPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `固定 Auto · ${site.name}` : "固定 Auto"}>
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
  const settings = useList<AutomationSetting>("automation-settings", {
    site_id: siteId,
    limit: 100,
  });
  const runs = useList<AutomationRun>(
    "automation-runs",
    { site_id: siteId, limit: 10 },
    { poll: true },
  );
  const runAction = useAction<AutomationRun>();
  const [editing, setEditing] = useState<StrategyRow | null>(null);
  const [editingAutomation, setEditingAutomation] =
    useState<StrategyRow | null>(null);
  const rows: StrategyRow[] = kinds.map(({ kind, name }) => ({
    id: kind,
    kind,
    name,
    strategy: query.data?.items.find((item) => item.kind === kind),
    automation: settings.data?.items.find(
      (item) => item.strategy_kind === kind,
    ),
  }));
  return (
    <>
      <DataTable
        items={rows}
        loading={query.isPending || settings.isPending}
        error={query.error ?? settings.error}
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
            key: "automation",
            title: "维护方式",
            render: (row) =>
              row.strategy ? (
                <Status value={row.automation?.mode ?? "manual"} />
              ) : (
                <span className="badge">未配置</span>
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
                {row.strategy && (
                  <IconButton
                    label={`设置 ${row.name} 自动调整`}
                    onClick={() => setEditingAutomation(row)}
                  >
                    <Bot size={16} />
                  </IconButton>
                )}
              </div>
            ),
          },
        ]}
      />
      <div className="section-title">
        <h2>最近调整</h2>
        <Button
          icon={<Play />}
          onClick={() =>
            runAction.mutate({
              path: "automation-runs",
              body: { site_id: siteId },
            })
          }
          pending={runAction.isPending}
        >
          评估最新评分
        </Button>
      </div>
      <Notice error={runAction.error ?? runs.error} />
      <DataTable
        items={runs.data?.items ?? []}
        loading={runs.isPending}
        error={runs.error}
        empty="尚无自动调整记录"
        columns={[
          {
            key: "time",
            title: "时间",
            render: (run) => new Date(run.completed_at).toLocaleString(),
          },
          {
            key: "summary",
            title: "结果",
            render: (run) => <span className="cell-title">{run.summary}</span>,
          },
          {
            key: "decisions",
            title: "决定",
            className: "numeric",
            render: (run) => run.decisions.length,
          },
          {
            key: "status",
            title: "状态",
            render: (run) => <Status value={run.status} />,
          },
        ]}
      />
      {editing && (
        <StrategyEditor
          siteId={siteId}
          row={editing}
          automation={editing.automation}
          onClose={() => setEditing(null)}
        />
      )}
      {editingAutomation && editingAutomation.strategy && (
        <AutomationEditor
          siteId={siteId}
          row={editingAutomation}
          onClose={() => setEditingAutomation(null)}
        />
      )}
    </>
  );
}

function StrategyEditor({
  siteId,
  row,
  automation,
  onClose,
}: {
  siteId: string;
  row: StrategyRow;
  automation?: AutomationSetting;
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
        {automation?.mode === "automatic" && (
          <div className="notice notice-warning" role="note">
            当前成员由自动调整维护。切回人工维护后才能修改成员。
          </div>
        )}
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
                  disabled={automation?.mode === "automatic"}
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
            disabled={
              relations.isPending ||
              !!relations.error ||
              automation?.mode === "automatic"
            }
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

function AutomationEditor({
  siteId,
  row,
  onClose,
}: {
  siteId: string;
  row: StrategyRow;
  onClose: () => void;
}) {
  const [automatic, setAutomatic] = useState(
    row.automation?.mode === "automatic",
  );
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `sites/${siteId}/automation/${row.kind}`,
      method: "PUT",
      body: {
        mode: automatic ? "automatic" : "manual",
        version: row.automation?.version ?? 0,
        reason,
      },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`维护方式 · ${row.strategy?.display_name ?? row.name}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <Checkbox
          label="自动调整成员"
          checked={automatic}
          onChange={(event) => setAutomatic(event.target.checked)}
        />
        {automatic && (
          <div className="notice notice-warning" role="note">
            开启后，完整评分批次可以生成并发布新的成员线路。
          </div>
        )}
        <Field label="修改原因">
          <Input
            required
            minLength={3}
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
            variant={automatic ? "danger" : "primary"}
            icon={automatic ? <Bot /> : <Save />}
            pending={action.isPending}
          >
            {automatic ? "开启自动调整" : "切回人工维护"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
