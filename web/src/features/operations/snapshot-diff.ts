export interface SnapshotChange {
  id: string;
  object: string;
  field: string;
  before: string;
  after: string;
}

export interface SnapshotComparison {
  initial: boolean;
  changes: SnapshotChange[];
  error?: string;
}

interface Resource {
  id: string;
  name: string;
  status: string;
  visible: boolean;
  ratio: string;
  baseURL: string;
  models: string[];
  credentialVersion: number;
  priority: number;
  weight: number;
  autoGroups: string[];
}

interface AutoGroup {
  id: string;
  name: string;
  visible: boolean;
  ratio: string;
  members: string[];
}

interface SnapshotView {
  siteID: string;
  resources: Map<string, Resource>;
  groups: Map<string, AutoGroup>;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("线路配置格式无法读取");
  return value as Record<string, unknown>;
}

function text(value: unknown): string {
  if (typeof value !== "string") throw new Error("线路配置包含无法读取的内容");
  return value;
}

function number(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value))
    throw new Error("线路配置包含无效数值");
  return value;
}

function boolean(value: unknown): boolean {
  if (typeof value !== "boolean") throw new Error("线路配置包含无效状态");
  return value;
}

function array(value: unknown): unknown[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error("线路配置包含无效列表");
  return value;
}

function parseSnapshot(value: unknown): SnapshotView {
  const snapshot = object(value);
  if (snapshot.schema_version !== 1 && snapshot.schema_version !== 2)
    throw new Error("当前控制台无法读取该线路版本");
  const rawResources =
    snapshot.schema_version === 1 ? [snapshot] : array(snapshot.resources);
  const resources = new Map<string, Resource>();
  for (const raw of rawResources) {
    const resource = object(raw);
    const group = object(resource.group);
    const channel = object(resource.channel);
    const id = text(resource.relation_id);
    if (resources.has(id)) throw new Error("线路配置包含重复供应商");
    resources.set(id, {
      id,
      name: text(group.display_name),
      status: text(channel.desired_status),
      visible: boolean(group.visible),
      ratio: text(group.sale_ratio),
      baseURL: text(channel.base_url),
      models: array(channel.models)
        .map((rawModel) => {
          const model = object(rawModel);
          const name = text(model.model);
          const upstream = text(model.upstream_model);
          return name === upstream ? name : `${name} → ${upstream}`;
        })
        .sort(),
      credentialVersion: number(channel.credential_version),
      priority: number(channel.priority),
      weight: number(channel.weight),
      autoGroups: array(channel.extra_group_keys).map(text).sort(),
    });
  }
  const groups = new Map<string, AutoGroup>();
  for (const raw of array(snapshot.auto_groups)) {
    const group = object(raw);
    const id = text(group.key);
    if (groups.has(id)) throw new Error("线路配置包含重复 Auto 分组");
    groups.set(id, {
      id,
      name: text(group.display_name),
      visible: boolean(group.visible),
      ratio: text(group.sale_ratio),
      members: [...resources.values()]
        .filter((resource) => resource.autoGroups.includes(id))
        .map((resource) => resource.id)
        .sort(),
    });
  }
  return { siteID: text(snapshot.site_id), resources, groups };
}

function status(value: string): string {
  if (value === "enabled") return "上线";
  if (value === "disabled") return "下线";
  return "待核对";
}

function visibility(value: boolean, auto = false): string {
  if (auto) return value ? "用户入口开放" : "用户入口关闭";
  return value ? "入口开放" : "入口关闭";
}
function list(values: string[]): string {
  return values.length ? values.join("、") : "无";
}

export function compareSnapshots(
  previous: unknown,
  current: unknown,
): SnapshotComparison {
  const initial = previous === undefined || previous === null;
  const changes: SnapshotChange[] = [];
  try {
    const next = parseSnapshot(current);
    const prior = initial
      ? {
          siteID: next.siteID,
          resources: new Map<string, Resource>(),
          groups: new Map<string, AutoGroup>(),
        }
      : parseSnapshot(previous);
    if (prior.siteID !== next.siteID)
      throw new Error("两个版本属于不同站点，无法比较");
    const add = (
      subject: string,
      field: string,
      before: string | undefined,
      after: string | undefined,
      force = false,
    ) => {
      if (!force && before === after) return;
      changes.push({
        id: String(changes.length),
        object: subject,
        field,
        before: before ?? "未设置",
        after: after ?? "已移除",
      });
    };
    for (const id of [
      ...new Set([...prior.resources.keys(), ...next.resources.keys()]),
    ].sort()) {
      const before = prior.resources.get(id);
      const after = next.resources.get(id);
      const subject = after?.name ?? before?.name ?? "供应商";
      if (!after) {
        add(subject, "供应商投放", "已投放", "已移除");
        continue;
      }
      if (!before) add(subject, "供应商投放", "未投放", "已投放");
      if (before) add(subject, "分组展示名", before.name, after.name);
      add(
        subject,
        "运营状态",
        before ? status(before.status) : undefined,
        status(after.status),
      );
      add(
        subject,
        "专属入口",
        before ? visibility(before.visible) : undefined,
        visibility(after.visible),
      );
      add(subject, "销售倍率", before?.ratio, after.ratio);
      add(subject, "上游地址", before?.baseURL, after.baseURL);
      add(
        subject,
        "模型",
        before ? list(before.models) : undefined,
        list(after.models),
      );
      add(
        subject,
        "密钥版本",
        before ? String(before.credentialVersion) : undefined,
        String(after.credentialVersion),
      );
      add(
        subject,
        "优先级",
        before ? String(before.priority) : undefined,
        String(after.priority),
      );
      add(
        subject,
        "权重",
        before ? String(before.weight) : undefined,
        String(after.weight),
      );
    }
    for (const id of [
      ...new Set([...prior.groups.keys(), ...next.groups.keys()]),
    ].sort()) {
      const before = prior.groups.get(id);
      const after = next.groups.get(id);
      const subject = `Auto · ${after?.name ?? before?.name ?? "分组"}`;
      if (!after) {
        add(subject, "Auto 分组", "已启用", "已移除");
        continue;
      }
      if (!before) add(subject, "Auto 分组", "未启用", "已启用");
      if (before) add(subject, "展示名称", before.name, after.name);
      add(
        subject,
        "用户入口",
        before ? visibility(before.visible, true) : undefined,
        visibility(after.visible, true),
      );
      add(subject, "销售倍率", before?.ratio, after.ratio);
      if (!before || list(before.members) !== list(after.members)) {
        const beforeNames = before
          ? list(
              before.members.map(
                (member) => prior.resources.get(member)?.name ?? "供应商",
              ),
            )
          : undefined;
        const afterNames = list(
          after.members.map(
            (member) => next.resources.get(member)?.name ?? "供应商",
          ),
        );
        add(
          subject,
          "供应商成员",
          beforeNames,
          beforeNames === afterNames
            ? `${afterNames}（成员已更换）`
            : afterNames,
          true,
        );
      }
    }
    return { initial, changes };
  } catch (error) {
    return {
      initial,
      changes: [],
      error: error instanceof Error ? error.message : "线路版本无法比较",
    };
  }
}
