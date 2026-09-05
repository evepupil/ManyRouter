import { useDeferredValue, useState, type FormEvent } from "react";
import { KeyRound, Pencil, Plus, Save, XCircle } from "lucide-react";
import { useAction, useList } from "../../api/hooks";
import type { Supplier, SupplierModel } from "../../api/types";
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
import { ModelEditor, emptyModel } from "./model-editor";
import { CancelCredential } from "./cancel-credential";

export function SuppliersPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [editing, setEditing] = useState<Supplier | "new" | null>(null);
  const [rotating, setRotating] = useState<Supplier | null>(null);
  const [canceling, setCanceling] = useState<Supplier | null>(null);
  const query = useList<Supplier>("suppliers", {
    q: useDeferredValue(search),
    offset,
  });
  return (
    <Page
      title="供应商"
      action={
        <Button
          variant="primary"
          icon={<Plus />}
          onClick={() => setEditing("new")}
        >
          新增供应商
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
        empty="尚未录入供应商"
        columns={[
          {
            key: "name",
            title: "供应商",
            render: (supplier) => (
              <>
                <div className="cell-title">{supplier.name}</div>
                <div className="cell-sub">{supplier.code}</div>
              </>
            ),
          },
          {
            key: "address",
            title: "上游地址",
            render: (supplier) => (
              <span className="cell-sub">{supplier.upstream_base_url}</span>
            ),
          },
          {
            key: "models",
            title: "模型",
            className: "numeric",
            render: (supplier) => supplier.models?.length ?? 0,
          },
          {
            key: "status",
            title: "状态",
            render: (supplier) => <Status value={supplier.status} />,
          },
          {
            key: "credential",
            title: "凭证版本",
            className: "numeric",
            render: (supplier) => (
              <>
                <div>{supplier.credential_version}</div>
                {supplier.pending_credential_version != null && (
                  <span className="badge badge-warning">
                    待同步 {supplier.pending_credential_version}
                  </span>
                )}
              </>
            ),
          },
          {
            key: "actions",
            title: "操作",
            render: (supplier) => (
              <div className="cell-actions">
                <IconButton
                  label={
                    supplier.pending_credential_version == null
                      ? `更换 ${supplier.name} 密钥`
                      : `替换 ${supplier.name} 候选密钥`
                  }
                  onClick={() => setRotating(supplier)}
                >
                  <KeyRound size={16} />
                </IconButton>
                {supplier.pending_credential_version != null && (
                  <IconButton
                    label={`取消 ${supplier.name} 候选密钥`}
                    onClick={() => setCanceling(supplier)}
                  >
                    <XCircle size={16} />
                  </IconButton>
                )}
                <IconButton
                  label={`编辑 ${supplier.name}`}
                  onClick={() => setEditing(supplier)}
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
        <SupplierEditor
          supplier={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      {rotating && (
        <CredentialEditor
          supplier={rotating}
          onClose={() => setRotating(null)}
        />
      )}
      {canceling && (
        <CancelCredential
          supplier={canceling}
          onClose={() => setCanceling(null)}
        />
      )}
    </Page>
  );
}

function SupplierEditor({
  supplier,
  onClose,
}: {
  supplier?: Supplier;
  onClose: () => void;
}) {
  const [code, setCode] = useState(supplier?.code ?? "");
  const [name, setName] = useState(supplier?.name ?? "");
  const [baseURL, setBaseURL] = useState(supplier?.upstream_base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [status, setStatus] = useState(supplier?.status ?? "enabled");
  const [models, setModels] = useState<SupplierModel[]>(
    supplier?.models?.length ? supplier.models : [emptyModel()],
  );
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const common = { name, upstream_base_url: baseURL, models };
    action.mutate(
      supplier
        ? {
            path: `suppliers/${supplier.id}`,
            method: "PUT",
            body: { ...common, version: supplier.version, status, reason },
          }
        : {
            path: "suppliers",
            body: { ...common, code, upstream_api_key: apiKey },
          },
    );
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={supplier ? `编辑供应商 · ${supplier.name}` : "新增供应商"}
      wide
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
          <Field label="供应商标识">
            <Input
              required
              disabled={!!supplier}
              minLength={2}
              maxLength={63}
              pattern={"[a-z0-9](?:[a-z0-9\\-]{0,61}[a-z0-9])?"}
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </Field>
          <Field label="上游地址" full>
            <Input
              type="url"
              required
              value={baseURL}
              onChange={(event) => setBaseURL(event.target.value)}
            />
          </Field>
          {!supplier && (
            <Field label="上游密钥" full>
              <Input
                type="password"
                required
                autoComplete="new-password"
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
              />
            </Field>
          )}
          {supplier && (
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
        </div>
        <ModelEditor models={models} onChange={setModels} />
        {supplier && (
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
            variant="primary"
            icon={<Save />}
            pending={action.isPending}
          >
            保存
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function CredentialEditor({
  supplier,
  onClose,
}: {
  supplier: Supplier;
  onClose: () => void;
}) {
  const [apiKey, setApiKey] = useState("");
  const [reason, setReason] = useState("");
  const action = useAction(onClose);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    action.mutate({
      path: `suppliers/${supplier.id}/credentials`,
      body: { version: supplier.version, api_key: apiKey, reason },
    });
  };
  return (
    <Modal
      open
      busy={action.isPending}
      onClose={onClose}
      title={`更换密钥 · ${supplier.name}`}
    >
      <form className="form-stack" onSubmit={submit}>
        <Field label="新密钥">
          <Input
            type="password"
            required
            autoComplete="new-password"
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>
        <Field label="更换原因">
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
            icon={<KeyRound />}
            pending={action.isPending}
          >
            更换密钥
          </Button>
        </div>
      </form>
    </Modal>
  );
}
