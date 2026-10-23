import { test } from "node:test";
import assert from "node:assert/strict";

import type { Budget, RunnerView } from "./backlog_model.js";
import {
  type Usage, applySpend, closedGate, parseUsage, pct, readingAge, resetLabel, runUse, tighterWindow, usageHover,
} from "./backlog_usage.js";

const NOW = Date.parse("2000-01-03T10:00:00");
const at = (s: string): string => new Date(s).toISOString();
const SOON = at("2000-01-03T14:00:00");
const MONDAY = at("2000-01-10T09:00:00");

const budget = (over: Partial<Budget> = {}): Budget => ({ share: 50, stop_at: 90, max_per_run: 10, usd_limit: 0, usd_spent: 0, ...over });

function usage(over: Partial<Usage> = {}): Usage {
  return {
    metering: false,
    five_hour: { plan: 41, backlog: 12, resets_at: SOON, all_cost: 20 },
    seven_day: { plan: 40, backlog: 18, resets_at: MONDAY, all_cost: 200 },
    reading_at: at("2000-01-03T09:58:00"),
    runs: {},
    unmeterable: {},
    ...over,
  };
}

test("parseUsage reads the GetBudget reply and fills what Go leaves out", () => {
  const u = parseUsage({
    budget: {}, metering: true, reading_at: null,
    windows: { five_hour: { plan: 3, backlog: 1, resets_at: SOON, all_cost: 2 }, seven_day: { plan: 0, backlog: 0, resets_at: null } },
    runs: { s1: 1.5, bad: "x" }, unmeterable: { s2: "the runner can't meter aider" },
  });
  assert.equal(u.metering, true);
  assert.deepEqual(u.five_hour, { plan: 3, backlog: 1, resets_at: SOON, all_cost: 2 });
  assert.equal(u.seven_day.resets_at, null);
  assert.deepEqual(u.runs, { s1: 1.5 });
  assert.deepEqual(u.unmeterable, { s2: "the runner can't meter aider" });
  assert.deepEqual(parseUsage(null).runs, {});
});

test("applySpend replaces windows and runs but keeps what the event does not carry", () => {
  const prev = usage({ metering: true, unmeterable: { s9: "x" } });
  const next = applySpend(prev, { five_hour: { plan: 50, backlog: 20, resets_at: SOON }, seven_day: {}, reading_at: null, runs: { s1: 2 } });
  assert.equal(next?.five_hour.backlog, 20);
  assert.equal(next?.seven_day.resets_at, null);
  assert.deepEqual(next?.runs, { s1: 2 });
  assert.deepEqual(next?.unmeterable, { s9: "x" });
  assert.equal(next?.metering, false);
  assert.equal(applySpend(prev, "junk"), prev);
});

test("the bar shows the window closest to stopping the runner", () => {
  assert.equal(tighterWindow(usage(), budget(), NOW), "five_hour", "plan 41/90 beats backlog 18/50");
  assert.equal(tighterWindow(usage({ seven_day: { plan: 40, backlog: 30, resets_at: MONDAY } }), budget(), NOW), "seven_day");
  assert.equal(tighterWindow(usage({ five_hour: { plan: 45, backlog: 0, resets_at: SOON },
    seven_day: { plan: 0, backlog: 25, resets_at: MONDAY } }), budget(), NOW), "five_hour", "a tie goes to the 5-hour window");
  assert.equal(tighterWindow(usage(), budget({ share: 0 }), NOW), "five_hour");
  assert.equal(tighterWindow(usage({ five_hour: { plan: 0, backlog: 0, resets_at: null } }), budget(), NOW), "seven_day");
  assert.equal(tighterWindow(usage({ five_hour: { plan: 9, backlog: 9, resets_at: at("2000-01-03T09:00:00") },
    seven_day: { plan: 0, backlog: 0, resets_at: null } }), budget(), NOW), null);
});

test("hover text shows both windows, the reading age and the scope", () => {
  assert.equal(usageHover(usage(), NOW), [
    "5-hour · plan 41% · backlog 12% · resets 14:00",
    "Weekly · plan 40% · backlog 18% · resets Mon 09:00",
    "read 2 min ago · all projects",
  ].join("\n"));
  assert.match(usageHover(usage({ seven_day: { plan: 0, backlog: 0, resets_at: null }, reading_at: null }), NOW),
    /Weekly · no reading\nno reading yet · all projects$/);
  assert.equal(resetLabel(SOON, NOW), "14:00");
  assert.equal(readingAge(at("2000-01-03T09:59:30"), NOW), "read just now");
  assert.equal(readingAge(at("2000-01-03T07:00:00"), NOW), "read 3 h ago");
  assert.equal(pct(12.5), "12.5%");
  assert.equal(pct(17.83), "17.8%");
});

test("run use is the run's part of the 5-hour window", () => {
  assert.equal(runUse(2, usage(), NOW), 4.1);
  assert.equal(runUse(2, usage({ five_hour: { plan: 41, backlog: 12, resets_at: null, all_cost: 20 } }), NOW), null);
  assert.equal(runUse(2, usage({ five_hour: { plan: 41, backlog: 12, resets_at: SOON, all_cost: 0 } }), NOW), null);
});

test("closedGate names the first closed budget gate", () => {
  const runner: RunnerView = { state: "off", used: 0, max: 1 };
  assert.equal(closedGate(budget(), usage(), runner, NOW), null);
  assert.equal(closedGate(budget({ share: 15 }), usage(), runner, NOW), "The backlog used its 15% of the week.");
  assert.equal(closedGate(budget({ share: 10 }), usage(), runner, NOW), "The backlog used its 10% of the 5-hour window.");
  assert.equal(closedGate(budget({ stop_at: 40 }), usage(), runner, NOW), "The plan is at 41% of the 5-hour window.");
  assert.equal(closedGate(budget({ share: 0 }), null, runner, NOW), "The backlog share is 0%.");
  assert.equal(closedGate(budget({ share: 10 }), usage({ five_hour: { plan: 41, backlog: 12, resets_at: at("2000-01-03T09:00:00") },
    seven_day: { plan: 0, backlog: 0, resets_at: null } }), runner, NOW), null);
  const limited: RunnerView = { state: "stopped", gate: "usage_limit", reason: "Usage limit until 14:00", used: 0, max: 1 };
  assert.equal(closedGate(budget(), usage(), limited, NOW), "Usage limit until 14:00.");
  assert.equal(closedGate(null, null, { ...limited, gate: "no_reading" }, NOW), null);
});
