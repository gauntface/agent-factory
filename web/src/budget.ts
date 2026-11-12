// The Claude plan budget the config view's Budget section shows: GetBudget's
// answer, and the words and meter for each account. No DOM and no I/O.

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

export const WINDOW_LABELS: Record<WindowKey, { bar: string; detail: string }> = {
  five_hour: { bar: "5-hour window", detail: "5-hour" },
  seven_day: { bar: "week", detail: "Weekly" },
};

const WINDOWS: readonly WindowKey[] = ["five_hour", "seven_day"];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

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

/** A GetBudget reply. */
export function parseBudget(d: unknown): Budget {
  const v = isRecord(d) ? d : {};
  const accounts = Array.isArray(v.accounts) ? v.accounts.map(parseAccount).filter((a) => a !== null) : [];
  return { limit: Number(v.limit) || 0, accounts };
}

export function valid(w: PlanWindow, now: number): boolean {
  return w.resets_at !== null && Date.parse(w.resets_at) > now;
}

/** A percent the way every surface shows it: 50%, 12.5%. */
export function pct(v: number): string {
  return `${Number(v.toFixed(1))}%`;
}

/** The window closest to the limit: the higher plan %; a tie goes to the
 *  5-hour window. null when neither window has a valid reading. */
export function tighterWindow(a: AccountBudget, now: number): WindowKey | null {
  let best: WindowKey | null = null;
  for (const k of WINDOWS) {
    if (valid(a[k], now) && (best === null || a[k].plan > a[best].plan)) best = k;
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

/** Both windows against the limit, then the reading's age. */
export function usageLines(a: AccountBudget, limit: number, now: number): string[] {
  const line = (k: WindowKey): string => {
    const w = a[k];
    const label = WINDOW_LABELS[k].detail;
    if (!valid(w, now)) return `${label} · no reading`;
    return `${label} · plan ${pct(w.plan)} of ${pct(limit)} limit · resets ${resetLabel(w.resets_at!, now)}`;
  };
  return [line("five_hour"), line("seven_day"), readingAge(a.reading_at, now)];
}

/** The meter for the tighter window: "Plan 62% of 5-hour window", its fill as a
 *  percent of the limit, and "limit 50%". null when there is no valid reading. */
export interface PlanMeter {
  before: string;
  fill: number;
  label: string;
  after: string;
}

export function planMeter(a: AccountBudget, limit: number, now: number): PlanMeter | null {
  const k = tighterWindow(a, now);
  if (k === null) return null;
  const plan = a[k].plan;
  return {
    before: `Plan ${pct(plan)} of ${WINDOW_LABELS[k].bar}`,
    fill: limit > 0 ? Math.min(100, (plan / limit) * 100) : 100,
    label: `${pct(plan)} of a ${pct(limit)} limit`,
    after: `limit ${pct(limit)}`,
  };
}

/** A daemon reason without its CLI hints: the limit is edited on the same
 *  page, so a clause naming an af command is dropped. */
export function webReason(reason: string): string {
  const out = reason
    .replace(/,?\s+or\s+[^;,·]*"af [^"]*"[^;,·]*/g, "")
    .replace(/(?:[;,]\s*|\s·\s)?[^;,·]*"af [^"]*"[^;,·]*/g, "")
    .trim();
  return out || reason;
}

export function gateLine(a: AccountBudget): string {
  if (a.gate.name === "") return "Budget-gated tasks run now";
  return `Budget-gated tasks skip · ${webReason(a.gate.reason)}`;
}

/** The account's heading; shown only when there is more than one account. */
export function accountLabel(a: AccountBudget): string {
  return a.default ? `${a.account} · new sessions use it` : a.account;
}
