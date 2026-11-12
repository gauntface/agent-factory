import assert from "node:assert/strict";
import { afterEach, test } from "node:test";

import { ApiError, getBudget } from "./api.js";

import {
  type AccountBudget,
  accountLabel,
  gateLine,
  parseBudget,
  pct,
  planMeter,
  readingAge,
  resetLabel,
  tighterWindow,
  usageLines,
} from "./budget.js";

// Local time, so the reset labels read the same in any zone.
const now = new Date(2026, 10, 6, 12, 0).getTime();
const at = (hours: number, minutes = 0): string => new Date(now + hours * 3_600_000 + minutes * 60_000).toISOString();

function account(over: Partial<AccountBudget> = {}): AccountBudget {
  return {
    account: "default",
    default: true,
    five_hour: { plan: 62, resets_at: at(2) },
    seven_day: { plan: 30, resets_at: at(96) },
    reading_at: at(0, -2),
    gate: { name: "", reason: "" },
    ...over,
  };
}

test("parseBudget reads the daemon's reply and drops what it cannot use", () => {
  const b = parseBudget({
    limit: 50,
    accounts: [
      {
        account: "work",
        default: true,
        five_hour: { plan: 62, resets_at: "2026-11-06T14:00:00Z" },
        seven_day: { plan: 0, resets_at: null },
        reading_at: "2026-11-06T12:00:00Z",
        gate: { name: "limit", reason: "Plan at 62%" },
      },
      { account: "" },
      "junk",
      { account: "spare" },
    ],
  });
  assert.equal(b.limit, 50);
  assert.deepEqual(b.accounts.map((a) => a.account), ["work", "spare"]);
  assert.deepEqual(b.accounts[0].five_hour, { plan: 62, resets_at: "2026-11-06T14:00:00Z" });
  assert.equal(b.accounts[0].seven_day.resets_at, null);
  assert.deepEqual(b.accounts[0].gate, { name: "limit", reason: "Plan at 62%" });
  assert.deepEqual(b.accounts[1], {
    account: "spare",
    default: false,
    five_hour: { plan: 0, resets_at: null },
    seven_day: { plan: 0, resets_at: null },
    reading_at: null,
    gate: { name: "", reason: "" },
  });
  assert.deepEqual(parseBudget(null), { limit: 0, accounts: [] });
});

test("pct trims trailing zeros", () => {
  assert.equal(pct(50), "50%");
  assert.equal(pct(12.5), "12.5%");
  assert.equal(pct(12.04), "12%");
});

test("tighterWindow picks the higher valid window, and the 5-hour one on a tie", () => {
  assert.equal(tighterWindow(account(), 75, now), "five_hour");
  assert.equal(tighterWindow(account({ seven_day: { plan: 70, resets_at: at(96) } }), 75, now), "seven_day");
  assert.equal(tighterWindow(account({ seven_day: { plan: 62, resets_at: at(96) } }), 75, now), "five_hour");
  assert.equal(tighterWindow(account({ five_hour: { plan: 90, resets_at: at(-1) } }), 75, now), "seven_day");
  assert.equal(
    tighterWindow(account({ five_hour: { plan: 0, resets_at: null }, seven_day: { plan: 0, resets_at: null } }), 75, now),
    null,
  );
});

test("tighterWindow picks the later reset when both windows are at the limit, as the gate does", () => {
  const both = account({ five_hour: { plan: 90, resets_at: at(2) }, seven_day: { plan: 60, resets_at: at(96) } });
  assert.equal(tighterWindow(both, 50, now), "seven_day");
  assert.equal(tighterWindow(both, 75, now), "five_hour");
});

test("resetLabel shows a time within a day, else the weekday too", () => {
  assert.equal(resetLabel(at(2), now), "14:00");
  assert.equal(resetLabel(at(96), now), "Tue 12:00");
});

test("readingAge", () => {
  assert.equal(readingAge(null, now), "no reading yet");
  assert.equal(readingAge(at(0), now), "read just now");
  assert.equal(readingAge(at(0, -2), now), "read 2m ago");
  assert.equal(readingAge(at(-3), now), "read 3h ago");
  assert.equal(readingAge(at(-50), now), "read 2d ago");
});

test("usageLines shows both windows against the limit and the reading age", () => {
  assert.deepEqual(usageLines(account(), 50, now), [
    "5-hour · plan 62% of 50% limit · resets 14:00",
    "Weekly · plan 30% of 50% limit · resets Tue 12:00",
    "read 2m ago",
  ]);
  assert.deepEqual(usageLines(account({ seven_day: { plan: 0, resets_at: null }, reading_at: null }), 75, now), [
    "5-hour · plan 62% of 75% limit · resets 14:00",
    "Weekly · no reading",
    "no reading yet",
  ]);
});

test("planMeter fills as a share of the limit, capped at full", () => {
  assert.deepEqual(planMeter(account({ five_hour: { plan: 25, resets_at: at(2) } }), 50, now), {
    before: "Plan 30% of week",
    fill: 60,
    ariaLabel: "30% of a 50% limit",
    after: "limit 50%",
  });
  assert.equal(planMeter(account(), 50, now)?.fill, 100);
  assert.equal(planMeter(account(), 0, now)?.fill, 100);
  assert.equal(planMeter(account({ five_hour: { plan: 0, resets_at: null }, seven_day: { plan: 0, resets_at: null } }), 50, now), null);
});

test("gateLine says whether budget-gated Claude tasks run", () => {
  assert.equal(gateLine(account()), "Budget-gated Claude tasks run now");
  assert.equal(
    gateLine(account({ gate: { name: "limit", reason: "Plan at 62% of the 5-hour window (limit 50%) · resumes 14:00" } })),
    "Budget-gated Claude tasks skip · Plan at 62% of the 5-hour window (limit 50%) · resumes 14:00",
  );
});

test("accountLabel marks the global default account", () => {
  assert.equal(accountLabel(account()), "default · global default");
  assert.equal(accountLabel(account({ account: "work", default: false })), "work");
});

afterEach(() => {
  delete (globalThis as { fetch?: unknown }).fetch;
});

test("getBudget posts an empty GetBudget and parses the reply; an older daemon's 404 throws", async () => {
  let url = "";
  let body = "";
  let status = 200;
  (globalThis as { fetch: unknown }).fetch = async (u: string, init: RequestInit): Promise<Response> => {
    url = u;
    body = String(init.body);
    return {
      ok: status === 200,
      status,
      statusText: "",
      json: async () => ({ data: status === 200 ? { limit: 50, accounts: [{ account: "default", default: true }] } : null, error: null }),
    } as unknown as Response;
  };
  const b = await getBudget("");
  assert.equal(url, "/v1/GetBudget");
  assert.equal(body, "{}");
  assert.equal(b.limit, 50);
  assert.deepEqual(b.accounts.map((a) => a.account), ["default"]);
  status = 404;
  await assert.rejects(getBudget(""), ApiError);
});
