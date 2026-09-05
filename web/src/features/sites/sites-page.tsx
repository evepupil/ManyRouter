import { useDeferredValue, useState, type FormEvent } from "react";
import { Pencil, Plus, PowerOff, Save } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type { Site } from "../../api/types";
import { DataTable, Pagination, Toolbar } from "../../components/data-table";
import {
  Button,
  Field,
  IconButton,
  Input,
  Modal,
  Notice,
  Select,
  Status,
} from "../../components/ui";
import { Page } from "../../components/page";

export function SitesPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [editing, setEditing] = useState<Site | "new" | null>(null);
  const query = useList<Site>("sites", { q: useDeferredValue(search), offset });
  return (
    <Page
      title="站点"
      action={
        <Button
          variant="primary"
          icon={<Plus />}
          onClick={() => setEditing("new")}
        >
          新增站点
        </Button>
      }
    >
      <Toolbar
        search={search}
        onSearch={(value) => {
          setSearch(value);
          setOffset(0);
        }}
        onRefresh={() => {
          void query.refetch();
        }}
      />
      <DataTable
        items={query.data?.items ?? []}
        loading={query.isPending}
        error={query.error}
        empty="尚未接入站点"
        columns={[
          {
            key: "name",
            title: "站点",
            render: (site) => (
              <>
                <div className="cell-title">{site.name}</div>
                <div className="cell-sub">{site.code}</div>
              </>
            ),
          },
          {
            key: "address",
            title: "地址",
            render: (site) => (
              <span className="cell-sub">{site.new_api_base_url}</span>
            ),
          },
          {
            key: "status",
            title: "状态",
            render: (site) => <Status value={site.status} />,
          },
          {
            key: "compatibility",
            title: "兼容结果",
            render: (site) => <Status value={site.compatibility_status} />,
          },
          {
            key: "actions",
            title: "操作",
            render: (site) => (
              <div className="cell-actions">
                <IconButton
                  label={`编辑 ${site.name}`}
                  onClick={() => setEditing(site)}
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
      {editing && (
        <SiteEditor
          site={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </Page>
  );
}

function SiteEditor({ site, onClose }: { site?: Site; onClose: () => void }) {
  const [code, setCode] = useState(site?.code ?? "");
  const [name, setName] = useState(site?.name ?? "");
  const [baseURL, setBaseURL] = useState(site?.new_api_base_url ?? "");
  const [adminID, setAdminID] = useState(String(site?.admin_user_id ?? 1));
  const [token, setToken] = useState("");
  const [status, setStatus] = useState(site?.status ?? "enabled");
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const common = {
      name,
      new_api_base_url: baseURL,
      admin_user_id: Number(adminID),
      ...(token ? { new_api_access_token: token } : {}),
    };
    action.mutate(
      site
        ? {
            path: `sites/${site.id}`,
            method: "PUT",
            body: { ...common, version: site.version, status, reason },
          }
        : { path: "sites", body: { ...common, code } },
    );
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={site ? `编辑站点 · ${site.name}` : "新增站点"}
    >
      <form className="form-stack" onSubmit={submit}>
        <div className="form-grid">
          <Field label="名称">
            <Input
              required
              maxLength={120}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <Field label="站点标识">
            <Input
              required
              disabled={!!site}
              minLength={2}
              maxLength={63}
              pattern={"[a-z0-9](?:[a-z0-9\\-]{0,61}[a-z0-9])?"}
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </Field>
          <Field label="New API 地址" full>
            <Input
              type="url"
              required
              value={baseURL}
              onChange={(event) => setBaseURL(event.target.value)}
            />
          </Field>
          <Field label="管理员编号">
            <Input
              type="number"
              required
              min="1"
              step="1"
              value={adminID}
              onChange={(event) => setAdminID(event.target.value)}
            />
          </Field>
          {site && (
            <Field label="状态">
              <Select
                value={status}
                onChange={(event) =>
                  setStatus(
                    event.target.value === "disabled" ? "disabled" : "enabled",
                  )
                }
              >
                <option value="enabled">启用</option>
                <option value="disabled">停用</option>
              </Select>
            </Field>
          )}
          <Field label={site ? "更换管理凭证（选填）" : "管理凭证"} full>
            <Input
              type="password"
              autoComplete="new-password"
              required={!site}
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </Field>
        </div>
        {site && (
          <Field label="修改原因">
            <Input
              required
              maxLength={500}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </Field>
        )}
        <Notice error={action.error} />
        <div className="form-actions">
          <Button onClick={onClose} disabled={action.isPending}>
            取消
          </Button>
          <Button
            type="submit"
            variant={site && status === "disabled" ? "danger" : "primary"}
            icon={site && status === "disabled" ? <PowerOff /> : <Save />}
            pending={action.isPending}
          >
            {site && status === "disabled" ? "保存并停用站点" : "保存"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
