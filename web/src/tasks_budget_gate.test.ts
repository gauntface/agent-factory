import assert from "node:assert/strict";
import test from "node:test";
import { budgetGateUnavailableReason, buildTask, type AddTaskInput } from "./tasks.js";

const input: AddTaskInput = {
  name: "Nightly", projectPath: "/repo", trigger: "cron", cron: "0 2 * * *",
  watchCmd: "", prompt: "Tidy", targetSession: "", program: "", budgetGated: true,
};

test("buildTask sends the budget gate for a cron task", () => {
  assert.equal(buildTask(input).budget_gated, true);
  assert.equal(buildTask({ ...input, budgetGated: false }).budget_gated, false);
  assert.equal(buildTask({ ...input, budgetGated: undefined }).budget_gated, false);
});

test("buildTask clears the budget gate for a watch task", () => {
  assert.equal(buildTask({ ...input, trigger: "watch", watchCmd: "tail -F log" }).budget_gated, false);
});

test("a watch trigger replaces the switch with its reason", () => {
  assert.equal(budgetGateUnavailableReason("cron"), null);
  assert.match(budgetGateUnavailableReason("watch")!, /scheduled/);
});
