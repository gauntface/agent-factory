// The Claude plan budget the config view's Budget section shows: GetBudget's
// answer, and the words and meter for each account. No DOM and no I/O.

import { isRecord, str } from "./parse.js";
import { formatDuration } from "./time.js";

export interface PlanWindow {
  plan: number;
  /** null when there is no valid reading for the window. */
  resets_at: string | null;
}

export interface BudgetGate {
  /** Empty when the gate is open. */
  name: string;
  reason: string;
}

export interface AccountBudget {
  /** The af account; "default" is the ambient Claude identity. */
  account: string;
  /** The account new Claude sessions launch with. */
  default: boolean;
  five_hour: PlanWindow;
  seven_day: PlanWindow;
  reading_at: string | null;
  gate: BudgetGate;
}

export interface Budget {
  limit: number;
  accounts: AccountBudget[];
}

export type WindowKey = "five_hour" | "seven_day";

const WINDOW_LABELS: Record<WindowKey, { bar: string; detail: string }> = {
  five_hour: { bar: "5-hour window", detail: "5-hour" },
  seven_day: { bar: "week", detail: "Weekly" },
};

const WINDOWS: readonly WindowKey[] = ["five_hour", "seven_day"];

function parseWindow(v: unknown): PlanWindow {
  const w = isRecord(v) ? v : {};
  return { plan: Number(w.plan) || 0, resets_at: typeof w.resets_at === "string" ? w.resets_at : null };
}

function parseAccount(v: unknown): AccountBudget | null {
  if (!isRecord(v) || typeof v.account !== "string" || v.account === "") return null;
  const g = isRecord(v.gate) ? v.gate : {};
  return {
    account: v.account,
    default: v.default === true,
    five_hour: parseWindow(v.five_hour),
    seven_day: parseWindow(v.seven_day),
    reading_at: typeof v.reading_at === "string" ? v.reading_at : null,
    gate: { name: str(g.name), reason: str(g.reason) },
  };
}

export function parseBudget(d: unknown): Budget {
  const v = isRecord(d) ? d : {};
  const accounts = Array.isArray(v.accounts) ? v.accounts.map(parseAccount).filter((a) => a !== null) : [];
  return { limit: Number(v.limit) || 0, accounts };
}

function hasLiveReading(w: PlanWindow, now: number): w is PlanWindow & { resets_at: string } {
  return w.resets_at !== null && Date.parse(w.resets_at) > now;
}

/** A percent the way every surface shows it: 50%, 12.5%. */
export function pct(v: number): string {
  return `${Number(v.toFixed(1))}%`;
}

/** The window the meter shows. When both are at the limit it is the one with
 *  the later reset, which is the one the gate names; otherwise the higher plan
 *  %, and a tie goes to the 5-hour window. null when neither window has a valid
 *  reading. */
export function tighterWindow(a: AccountBudget, limit: number, now: number): WindowKey | null {
  let best: WindowKey | null = null;
  for (const k of WINDOWS) {
    const w = a[k];
    if (!hasLiveReading(w, now)) continue;
    if (best === null) {
      best = k;
      continue;
    }
    const b = a[best];
    const bothOver = w.plan >= limit && b.plan >= limit;
    if (bothOver ? Date.parse(w.resets_at) > Date.parse(b.resets_at!) : w.plan > b.plan) best = k;
  }
  return best;
}

/** A reset time: 14:00 within a day, else Mon 09:00, in local time.
 *  Mirrors planusage/gate.go resetLabel. */
export function resetLabel(at: string, now: number): string {
  const d = new Date(at);
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  if (d.getTime() - now < 24 * 3_600_000) return hm;
  return `${d.toLocaleDateString("en-US", { weekday: "short" })} ${hm}`;
}

export function readingAge(at: string | null, now: number): string {
  if (!at) return "no reading yet";
  const age = now - Date.parse(at);
  if (age < 60_000) return "read just now";
  return `read ${formatDuration(age)} ago`;
}

/** Both windows against the limit, then the reading's age. */
export function usageLines(a: AccountBudget, limit: number, now: number): string[] {
  const line = (k: WindowKey): string => {
    const w = a[k];
    const label = WINDOW_LABELS[k].detail;
    if (!hasLiveReading(w, now)) return `${label} · no reading`;
    return `${label} · plan ${pct(w.plan)} of ${pct(limit)} limit · resets ${resetLabel(w.resets_at, now)}`;
  };
  return [line("five_hour"), line("seven_day"), readingAge(a.reading_at, now)];
}

export interface PlanMeter {
  before: string;
  /** The plan as a percent of the limit, capped at 100. */
  fill: number;
  ariaLabel: string;
  after: string;
}

/** The meter for the tighter window: "Plan 62% of 5-hour window", the fill, and
 *  "limit 50%". null when there is no valid reading. */
export function planMeter(a: AccountBudget, limit: number, now: number): PlanMeter | null {
  const k = tighterWindow(a, limit, now);
  if (k === null) return null;
  const plan = a[k].plan;
  return {
    before: `Plan ${pct(plan)} of ${WINDOW_LABELS[k].bar}`,
    fill: limit > 0 ? Math.min(100, (plan / limit) * 100) : 100,
    ariaLabel: `${pct(plan)} of a ${pct(limit)} limit`,
    after: `limit ${pct(limit)}`,
  };
}

export function gateLine(a: AccountBudget): string {
  if (a.gate.name === "") return "Budget-gated Claude tasks run now";
  return `Budget-gated Claude tasks skip · ${a.gate.reason}`;
}

/** The account's heading; shown only when there is more than one account. */
export function accountLabel(a: AccountBudget): string {
  return a.default ? `${a.account} · global default` : a.account;
}
