import { test } from "node:test";
import assert from "node:assert/strict";

import type { BacklogView, RunnerView } from "./backlog_model.js";
import { runnerWords } from "./backlog_runner.js";

function view(on: boolean, runner: Partial<RunnerView>): BacklogView {
  return {
    backlog: { schema_version: 1, next_id: 1, settings: { runner: on, max_running: 2, on_done: "keep" }, items: [] },
    states: {},
    runner: { state: "idle", used: 0, max: 2, ...runner },
  };
}

test("the switch label follows the setting, never the runner state", () => {
  assert.equal(runnerWords(view(false, { state: "off" })).label, "Runner off");
  for (const state of ["idle", "running", "waiting", "stopped"]) {
    assert.equal(runnerWords(view(true, { state })).label, "Runner on", state);
  }
});

test("an off runner has no status word and keeps its reason", () => {
  assert.deepEqual(runnerWords(view(false, { state: "off", reason: "Items run only when you start them." })),
    { label: "Runner off", status: "", reason: "Items run only when you start them", warn: false });
});

test("a running runner counts its live runs", () => {
  assert.deepEqual(runnerWords(view(true, { state: "running", used: 2 })),
    { label: "Runner on", status: "2 running", reason: "", warn: false });
});

test("an idle runner says nothing is queued or just idle", () => {
  assert.equal(runnerWords(view(true, { state: "idle", reason: "Nothing queued." })).status, "Nothing queued");
  assert.equal(runnerWords(view(true, { state: "idle" })).status, "Idle");
  assert.equal(runnerWords(view(true, { state: "idle" })).warn, false);
});

test("a closed gate warns and keeps the reason", () => {
  const stopped = runnerWords(view(true, { state: "stopped", gate: "limit", reason: "Plan at 52% of the 5-hour window (limit 50%) · resumes 14:00" }));
  assert.deepEqual(stopped, {
    label: "Runner on", status: "Blocked", reason: "Plan at 52% of the 5-hour window (limit 50%) · resumes 14:00", warn: true,
  });
  const waiting = runnerWords(view(true, { state: "waiting", gate: "no_reading", reason: "No plan usage seen yet." }));
  assert.equal(waiting.status, "Waiting");
  assert.equal(waiting.reason, "No plan usage seen yet");
  assert.equal(waiting.warn, true);
});
