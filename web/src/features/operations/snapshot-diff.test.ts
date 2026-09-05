import { deepEqual, equal, ok } from "node:assert/strict";
import { test } from "node:test";
import { compareSnapshots } from "./snapshot-diff.ts";

function resource(id = "relation-a", name = "供应商甲") {
  return {
    schema_version: 1,
    site_id: "site-a",
    relation_id: id,
    group: {
      key: `group-${id}`,
      display_name: name,
      sale_ratio: "1.25",
      visible: true,
    },
    channel: {
      name,
      base_url: "https://upstream.example",
      desired_status: "enabled",
      credential_version: 1,
      credential_id: "private-credential-id",
      models: [{ model: "model-a", upstream_model: "model-a" }],
      priority: 0,
      weight: 100,
      extra_group_keys: ["auto-balanced"],
    },
  };
}

function snapshot() {
  return {
    schema_version: 2,
    site_id: "site-a",
    resources: [resource()],
    auto_groups: [
      {
        key: "auto-balanced",
        display_name: "均衡",
        sale_ratio: "1.5",
        visible: true,
      },
    ],
  };
}

test("first configuration presents readable business fields without credential identifiers", () => {
  const result = compareSnapshots(null, snapshot());
  equal(result.initial, true);
  equal(result.error, undefined);
  ok(
    result.changes.some(
      (change) => change.field === "供应商投放" && change.after === "已投放",
    ),
  );
  ok(
    result.changes.some(
      (change) => change.field === "供应商成员" && change.after === "供应商甲",
    ),
  );
  ok(!JSON.stringify(result).includes("private-credential-id"));
});

test("detects state, visibility, model mapping, price and credential-only changes", () => {
  const before = snapshot();
  const after = snapshot();
  const changed = after.resources[0];
  ok(changed);
  changed.channel.desired_status = "disabled";
  changed.channel.credential_version = 2;
  changed.channel.models = [{ model: "model-a", upstream_model: "model-b" }];
  changed.group.visible = false;
  changed.group.sale_ratio = "1.35";
  const result = compareSnapshots(before, after);
  deepEqual(
    result.changes.map((change) => change.field),
    ["运营状态", "专属入口", "销售倍率", "模型", "密钥版本"],
  );
  equal(
    result.changes.find((change) => change.field === "密钥版本")?.after,
    "2",
  );
  equal(
    result.changes.find((change) => change.field === "专属入口")?.after,
    "入口关闭",
  );
});

test("compares Auto membership by relation identity rather than supplier label", () => {
  const before = snapshot();
  const after = snapshot();
  after.resources.push(resource("relation-b", "供应商乙"));
  const first = after.resources[0];
  ok(first);
  first.channel.extra_group_keys = [];
  const membership = compareSnapshots(before, after).changes.find(
    (change) => change.field === "供应商成员",
  );
  equal(membership?.before, "供应商甲");
  equal(membership?.after, "供应商乙");
});

test("model ordering and M0 to M1 envelope changes do not create false changes", () => {
  const legacy = resource();
  legacy.channel.extra_group_keys = [];
  legacy.channel.models.push({ model: "model-b", upstream_model: "model-b" });
  const modern = {
    schema_version: 2,
    site_id: "site-a",
    resources: [structuredClone(legacy)],
    auto_groups: [],
  };
  modern.resources[0]?.channel.models.reverse();
  deepEqual(compareSnapshots(legacy, modern).changes, []);
});

test("does not hide membership changes when two suppliers have the same display name", () => {
  const before = snapshot();
  const after = snapshot();
  after.resources = [resource("relation-b", "供应商甲")];
  const change = compareSnapshots(before, after).changes.find(
    (item) => item.field === "供应商成员",
  );
  equal(change?.after, "供应商甲（成员已更换）");
});

test("rejects cross-site and unsupported snapshots instead of showing an empty comparison", () => {
  const after = snapshot();
  after.site_id = "site-b";
  equal(
    compareSnapshots(snapshot(), after).error,
    "两个版本属于不同站点，无法比较",
  );
  equal(
    compareSnapshots(null, { schema_version: 99 }).error,
    "当前控制台无法读取该线路版本",
  );
});

test("records removed suppliers and Auto groups", () => {
  const after = { ...snapshot(), resources: [], auto_groups: [] };
  deepEqual(
    compareSnapshots(snapshot(), after).changes.map((change) => [
      change.field,
      change.after,
    ]),
    [
      ["供应商投放", "已移除"],
      ["Auto 分组", "已移除"],
    ],
  );
});
