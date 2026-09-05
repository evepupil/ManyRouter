import { deepEqual } from "node:assert/strict";
import { test } from "node:test";
import { resolveDeploymentSelection } from "./selection.ts";

const sites = [
  { id: "site-a", status: "enabled" },
  { id: "site-b", status: "disabled" },
  { id: "site-c", status: "enabled" },
];

test("defaults to the current site only when the site is enabled", () => {
  deepEqual(resolveDeploymentSelection(sites, null, "site-a"), ["site-a"]);
  deepEqual(resolveDeploymentSelection(sites, null, "site-b"), []);
  deepEqual(resolveDeploymentSelection(sites, null, "missing"), []);
});

test("removes disabled, missing and duplicate sites from an existing selection", () => {
  deepEqual(
    resolveDeploymentSelection(
      sites,
      ["site-c", "site-b", "missing", "site-c", "site-a"],
      "site-a",
    ),
    ["site-c", "site-a"],
  );
});

test("a deliberate empty selection remains empty after new sites load", () => {
  deepEqual(resolveDeploymentSelection(sites, [], "site-a"), []);
});

test("a site disabled after selection cannot remain a deployment target", () => {
  deepEqual(
    resolveDeploymentSelection(
      [{ id: "site-a", status: "disabled" }],
      ["site-a"],
      "site-a",
    ),
    [],
  );
});
