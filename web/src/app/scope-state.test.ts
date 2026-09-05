import { equal } from "node:assert/strict";
import { test } from "node:test";
import { resolveScopeState } from "./scope-state.ts";

test("site request failure takes precedence over an empty selection and stale data", () => {
  equal(resolveScopeState(false, true, 0, ""), "error");
  equal(resolveScopeState(false, true, 2, "previous-site"), "error");
});

test("site request loading is distinct from successful empty results", () => {
  equal(resolveScopeState(true, false, 0, ""), "loading");
  equal(resolveScopeState(false, false, 0, ""), "empty");
});

test("successful nonempty site queries distinguish selection and ready state", () => {
  equal(resolveScopeState(false, false, 1, ""), "selection");
  equal(resolveScopeState(false, false, 1, "site-a"), "ready");
});
