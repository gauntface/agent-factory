// Plan usage for the Backlog view (backlog proposal, technical.md "Budget"):
// the GetBudget windows, backlog.spend payloads applied on top, the runner
// bar's window and hover text, run use, and the budget gates a start by hand
// asks about. No DOM and no I/O.

import type { Budget, RunnerView } from "./backlog_model.js";

export interface PlanWindow {
  plan: number;
  backlog: number;
  /** null when there is no valid reading for the window. */
  resets_at: string | null;
  /** The metered cost of every transcript in the window, in US dollars. */
  all_cost?: number;
}

export interface Usage {
  metering: boolean;
  five_hour: PlanWindow;
  seven_day: PlanWindow;
  reading_at: string | null;
  /** Each item session's metered cost in US dollars at API prices. */
  runs: Record<string, number>;
  /** Why af cannot meter an item session, by session id. */
  unmeterable: Record<string, string>;
}

export type WindowKey = "five_hour" | "seven_day";

export const WINDOW_LABELS: Record<WindowKey, { bar: string; gate: string; hover: string }> = {
  five_hour: { bar: "5-hour window", gate: "the 5-hour window", hover: "5-hour" },
  seven_day: { bar: "week", gate: "the week", hover: "Weekly" },
};

const WINDOWS: readonly WindowKey[] = ["five_hour", "seven_day"];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function numbers(v: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  if (isRecord(v)) for (const [k, x] of Object.entries(v)) if (typeof x === "number") out[k] = x;
  return out;
}

function strings(v: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (isRecord(v)) for (const [k, x] of Object.entries(v)) if (typeof x === "string") out[k] = x;
  return out;
}

function parseWindow(v: unknown): PlanWindow {
  const w = isRecord(v) ? v : {};
  return {
    plan: Number(w.plan) || 0,
    backlog: Number(w.backlog) || 0,
    resets_at: typeof w.resets_at === "string" ? w.resets_at : null,
    all_cost: Number(w.all_cost) || 0,
  };
}

/** The usage half of a GetBudget or SetBudget reply. */
export function parseUsage(d: unknown): Usage {
  const v = isRecord(d) ? d : {};
  const w = isRecord(v.windows) ? v.windows : {};
  return {
    metering: v.metering === true,
    five_hour: parseWindow(w.five_hour),
    seven_day: parseWindow(w.seven_day),
    reading_at: typeof v.reading_at === "string" ? v.reading_at : null,
    runs: numbers(v.runs),
    unmeterable: strings(v.unmeterable),
  };
}

/** Applies a backlog.spend payload; it carries the windows, reading time and run
 *  costs, so the rest stays as GetBudget last said. */
export function applySpend(prev: Usage | null, ev: unknown): Usage | null {
  if (!isRecord(ev)) return prev;
  return {
    metering: false,
    unmeterable: prev?.unmeterable ?? {},
    five_hour: parseWindow(ev.five_hour),
    seven_day: parseWindow(ev.seven_day),
    reading_at: typeof ev.reading_at === "string" ? ev.reading_at : null,
    runs: numbers(ev.runs),
  };
}

export function valid(w: PlanWindow, now: number): boolean {
  return w.resets_at !== null && Date.parse(w.resets_at) > now;
}

/** A percent the way every surface shows it: 50%, 12.5%. */
export function pct(v: number): string {
  return `${Number(v.toFixed(1))}%`;
}

export function usd(v: number): string {
  return `$${v.toFixed(2)}`;
}

/** The window the backlog is closer to its share in: the higher backlog
 *  percent, as one share covers both; a tie goes to the higher plan percent.
 *  null when neither window has a valid reading. */
export function tighterWindow(u: Usage, now: number): WindowKey | null {
  let best: WindowKey | null = null;
  for (const k of WINDOWS) {
    if (!valid(u[k], now)) continue;
    const b = best === null ? null : u[best];
    if (!b || u[k].backlog > b.backlog || (u[k].backlog === b.backlog && u[k].plan > b.plan)) best = k;
  }
  return best;
}

/** A reset time: 14:00 within a day, else Mon 09:00, in local time. */
export function resetLabel(at: string, now: number): string {
  const d = new Date(at);
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  if (d.getTime() - now < 24 * 3_600_000) return hm;
  return `${d.toLocaleDateString("en-US", { weekday: "short" })} ${hm}`;
}

export function readingAge(at: string | null, now: number): string {
  if (!at) return "no reading yet";
  const min = Math.floor(Math.max(0, now - Date.parse(at)) / 60_000);
  if (min < 1) return "read just now";
  if (min < 60) return `read ${min} min ago`;
  const hours = Math.floor(min / 60);
  return hours < 24 ? `read ${hours} h ago` : `read ${Math.floor(hours / 24)} d ago`;
}

/** The runner bar's hover: both windows, the reading's age, and the scope. */
export function usageHover(u: Usage, now: number): string {
  const line = (k: WindowKey): string => {
    const w = u[k];
    const label = WINDOW_LABELS[k].hover;
    if (!valid(w, now)) return `${label} · no reading`;
    return `${label} · plan ${pct(w.plan)} · backlog ${pct(w.backlog)} · resets ${resetLabel(w.resets_at!, now)}`;
  };
  return [line("five_hour"), line("seven_day"), `${readingAge(u.reading_at, now)} · all projects`].join("\n");
}

/** A run's use as a percent of the 5-hour window: the plan percent times the
 *  run's part of all metered cost in it. null without a reading or cost. */
export function runUse(cost: number, u: Usage, now: number): number | null {
  const w = u.five_hour;
  if (!valid(w, now) || !w.all_cost || w.all_cost <= 0) return null;
  return Math.min(100, (w.plan * cost) / w.all_cost);
}

/** The first closed budget gate, as one sentence, or null when all are open.
 *  A start by hand passes the gates, so the web asks once before it does. */
export function closedGate(b: Budget | null, u: Usage | null, runner: RunnerView | null, now: number): string | null {
  if (b && b.share <= 0) return "The backlog share is 0%.";
  if (b && u) {
    for (const k of WINDOWS) {
      const w = u[k];
      if (valid(w, now) && w.backlog >= b.share) return `The backlog used its ${pct(b.share)} of ${WINDOW_LABELS[k].gate}.`;
    }
    for (const k of WINDOWS) {
      const w = u[k];
      if (valid(w, now) && w.plan >= b.stop_at) return `The plan is at ${pct(w.plan)} of ${WINDOW_LABELS[k].gate}.`;
    }
  }
  if (runner?.state === "stopped" && (runner.gate === "usage_limit" || runner.gate === "usd_limit") && runner.reason) {
    return `${runner.reason.replace(/\.$/, "")}.`;
  }
  return null;
}
