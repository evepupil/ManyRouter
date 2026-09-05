import { equal, notEqual } from "node:assert/strict";
import { test } from "node:test";
import {
  confirmRequestAttempt,
  prepareRequestAttempt,
} from "./request-attempt.ts";

const action = {
  path: "prices",
  body: {
    site_id: "site-a",
    group_key: "group-a",
    sale_ratio: "1.25",
    reason: "调整售价",
  },
};

test("an unconfirmed request reuses its identifier after network or HTTP failure", () => {
  const first = prepareRequestAttempt(null, action, () => "request-a");
  const retried = prepareRequestAttempt(
    first,
    { ...action, body: { ...action.body } },
    () => "request-b",
  );
  equal(retried.key, first.key);
  equal(retried, first);
});

test("changing content, path or method creates a new request identifier", () => {
  const first = prepareRequestAttempt(null, action, () => "request-a");
  for (const changed of [
    { ...action, body: { ...action.body, sale_ratio: "1.5" } },
    { ...action, path: "plans/plan-a/restore" },
    { ...action, method: "PUT" },
  ]) {
    notEqual(
      prepareRequestAttempt(first, changed, () => "request-b").key,
      first.key,
    );
  }
  equal(
    prepareRequestAttempt(
      first,
      { ...action, method: "post" },
      () => "request-b",
    ).key,
    first.key,
  );
});

test("a confirmed success allows a later identical action to use a new identifier", () => {
  const first = prepareRequestAttempt(null, action, () => "request-a");
  const cleared = confirmRequestAttempt(first, first.key);
  equal(cleared, null);
  equal(
    prepareRequestAttempt(cleared, action, () => "request-b").key,
    "request-b",
  );
});

test("an older success cannot clear the identifier of a newer pending action", () => {
  const first = prepareRequestAttempt(null, action, () => "request-a");
  const newer = prepareRequestAttempt(
    first,
    { ...action, body: { ...action.body, sale_ratio: "2" } },
    () => "request-b",
  );
  equal(confirmRequestAttempt(newer, first.key), newer);
});
