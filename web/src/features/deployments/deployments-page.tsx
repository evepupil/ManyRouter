import { useDeferredValue, useEffect, useState, type FormEvent } from "react";
import { Network, Pencil, Plus, PowerOff, Save } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type { Relation, Site, Supplier } from "../../api/types";
import { useScope } from "../../app/scope";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
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
  Select,
  Status,
} from "../../components/ui";
import { Page, SiteRequired } from "../../components/page";
import { EntryAccess } from "../../components/entry-access";
import { resolveDeploymentSelection } from "./selection";

export function DeploymentsPage() {
  const { siteId, site } = useScope();
  return (
    <Page title={site ? `站点投放 · ${site.name}` : "站点投放"}>
      <SiteRequired>
        <DeploymentList key={siteId} siteId={siteId} />
      </SiteRequired>
    </Page>
  );
}

function DeploymentList({ siteId }: { siteId: string }) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [creating, setCreating] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [editing, setEditing] = useState<Relation | null>(null);
  const query = useList<Relation>("relations", {
    site_id: siteId,
    q: useDeferredValue(search),
    offset,
  });
  const suppliers = useList<Supplier>("suppliers", { limit: 100 });
  const supplierName = (relation: Relation) =>
    relation.supplier_name ??
    suppliers.data?.items.find(
      (supplier) => supplier.id === relation.supplier_id,
    )?.name ??
    relation.group_display_name;
  return (
    <>
      <Notice success={submitted ? "已提交同步" : undefined} />
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
          投放供应商
        </Button>
      </Toolbar>
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="当前站点尚未投放供应商"
        columns={[
          {
            key: "supplier",
            title: "供应商",
            render: (relation) => (
              <>
                <div className="cell-title">{supplierName(relation)}</div>
                <div className="cell-sub">{relation.group_display_name}</div>
              </>
            ),
          },
          {
            key: "ratio",
            title: "销售倍率",
            className: "numeric",
            render: (relation) => relation.sale_ratio,
          },
          {
            key: "visible",
            title: "专属入口",
            render: (relation) => (relation.visible ? "入口开放" : "入口关闭"),
          },
          {
            key: "desired",
            title: "运营状态",
            render: (relation) => <Status value={relation.desired_status} />,
          },
          {
            key: "sync",
            title: "同步结果",
            render: (relation) => <Status value={relation.sync_status} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (relation) => (
              <div className="cell-actions">
                <IconButton
                  label={`编辑 ${supplierName(relation)} 投放`}
                  onClick={() => setEditing(relation)}
                >
                  <Pencil size={16} />
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
      {creating && (
        <DeploymentEditor
          initialSiteId={siteId}
          onClose={() => setCreating(false)}
          onSaved={() => {
            setSubmitted(true);
            setCreating(false);
          }}
        />
      )}
      {editing && (
        <RelationEditor relation={editing} onClose={() => setEditing(null)} />
      )}
    </>
  );
}

interface SiteSettings {
  group_display_name: string;
  sale_ratio: string;
  visible: boolean;
}

function DeploymentEditor({
  initialSiteId,
  onClose,
  onSaved,
}: {
  initialSiteId: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const sites = useList<Site>("sites", { limit: 100 });
  const suppliers = useList<Supplier>("suppliers", { limit: 100 });
  const [supplierId, setSupplierId] = useState("");
  const [selected, setSelected] = useState<string[] | null>(null);
  const [settings, setSettings] = useState<Record<string, SiteSettings>>({});
  const [reason, setReason] = useState("");
  const action = useAction(onSaved);
  const selectedSites = resolveDeploymentSelection(
    sites.data?.items ?? [],
    selected,
    initialSiteId,
  );
  const candidatesLoading = sites.isPending || suppliers.isPending;
  const candidatesError = sites.error ?? suppliers.error;
  const enabledSuppliers =
    suppliers.data?.items.filter((supplier) => supplier.status === "enabled") ??
    [];
  const supplierEligible = enabledSuppliers.some(
    (supplier) => supplier.id === supplierId,
  );
  useEffect(() => {
    if (!sites.isSuccess) return;
    setSelected((current) => {
      const next = resolveDeploymentSelection(
        sites.data.items,
        current,
        initialSiteId,
      );
      return current &&
        current.length === next.length &&
        current.every((id, index) => id === next[index])
        ? current
        : next;
    });
  }, [sites.data, sites.isSuccess, initialSiteId]);
  const supplierName =
    suppliers.data?.items.find((supplier) => supplier.id === supplierId)
      ?.name ?? "";
  const getSettings = (id: string): SiteSettings =>
    settings[id] ?? {
      group_display_name: supplierName,
      sale_ratio: "",
      visible: true,
    };
  const update = (id: string, patch: Partial<SiteSettings>) =>
    setSettings((value) => ({
      ...value,
      [id]: { ...getSettings(id), ...patch },
    }));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (
      candidatesLoading ||
      candidatesError ||
      !selectedSites.length ||
      !supplierEligible
    )
      return;
    action.mutate({
      path: "deployments",
      body: {
        supplier_id: supplierId,
        sites: selectedSites.map((id) => ({ site_id: id, ...getSettings(id) })),
        reason,
      },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title="投放供应商"
      wide
    >
      <form className="form-stack" onSubmit={submit}>
        <Field label="供应商">
          <Select
            required
            disabled={suppliers.isPending || suppliers.isError}
            value={supplierId}
            onChange={(event) => setSupplierId(event.target.value)}
          >
            <option value="">
              {suppliers.isPending ? "正在读取供应商" : "选择供应商"}
            </option>
            {enabledSuppliers.map((supplier) => (
              <option value={supplier.id} key={supplier.id}>
                {supplier.name}
              </option>
            ))}
          </Select>
        </Field>
        {candidatesLoading && <Loading />}
        <Notice error={candidatesError} />
        {candidatesError && (
          <Button
            onClick={() => {
              void sites.refetch();
              void suppliers.refetch();
            }}
            pending={sites.isFetching || suppliers.isFetching}
          >
            重新读取候选项
          </Button>
        )}
        {suppliers.isSuccess && enabledSuppliers.length === 0 && (
          <Empty title="暂无启用的供应商" />
        )}
        <div className="form-stack">
          {!sites.isPending &&
            !sites.isError &&
            sites.data?.items.map((site) => {
              const checked = selectedSites.includes(site.id);
              const setting = getSettings(site.id);
              return (
                <div key={site.id} className="form-stack">
                  <div className="selection-item">
                    <Checkbox
                      label={site.name}
                      disabled={site.status !== "enabled"}
                      checked={checked}
                      onChange={(event) =>
                        setSelected((value) =>
                          event.target.checked
                            ? [...(value ?? selectedSites), site.id]
                            : (value ?? selectedSites).filter(
                                (id) => id !== site.id,
                              ),
                        )
                      }
                    />
                    <Status value={site.status} />
                  </div>
                  {checked && (
                    <div className="form-grid">
                      <Field label="分组展示名">
                        <Input
                          required
                          maxLength={120}
                          value={setting.group_display_name}
                          onChange={(event) =>
                            update(site.id, {
                              group_display_name: event.target.value,
                            })
                          }
                        />
                      </Field>
                      <Field label="销售倍率">
                        <Input
                          required
                          inputMode="decimal"
                          pattern="[0-9]+(\.[0-9]{1,6})?"
                          value={setting.sale_ratio}
                          onChange={(event) =>
                            update(site.id, { sale_ratio: event.target.value })
                          }
                        />
                      </Field>
                      <EntryAccess
                        open={setting.visible}
                        onChange={(visible) => update(site.id, { visible })}
                        full
                      />
                    </div>
                  )}
                </div>
              );
            })}
          {sites.isSuccess && sites.data.items.length === 0 && (
            <Empty title="尚未接入站点" />
          )}
        </div>
        <Field label="投放原因">
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
            variant="primary"
            icon={<Network />}
            pending={action.isPending}
            disabled={
              candidatesLoading ||
              !selectedSites.length ||
              !supplierEligible ||
              !!candidatesError
            }
          >
            确认投放
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function RelationEditor({
  relation,
  onClose,
}: {
  relation: Relation;
  onClose: () => void;
}) {
  const [name, setName] = useState(relation.group_display_name);
  const [visible, setVisible] = useState(relation.visible);
  const [status, setStatus] = useState(
    relation.desired_status === "disabled" ? "disabled" : "enabled",
  );
  const [resume, setResume] = useState(false);
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `relations/${relation.id}`,
      method: "PUT",
      body: {
        version: relation.version,
        group_display_name: name,
        visible,
        desired_status: status,
        resume,
        reason,
      },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`编辑投放 · ${relation.group_display_name}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <Field label="分组展示名">
          <Input
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="运营状态">
          <Select
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="enabled">上线</option>
            <option value="disabled">下线</option>
          </Select>
        </Field>
        <EntryAccess open={visible} onChange={setVisible} />
        {relation.sync_status === "manual_locked" && (
          <Checkbox
            label="恢复人工停用的渠道"
            checked={resume}
            onChange={(event) => setResume(event.target.checked)}
          />
        )}
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
            variant={status === "disabled" || !visible ? "danger" : "primary"}
            icon={status === "disabled" ? <PowerOff /> : <Save />}
            pending={action.isPending}
          >
            {status === "disabled"
              ? "保存并下线供应商"
              : visible
                ? "保存投放"
                : "保存并关闭入口"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
