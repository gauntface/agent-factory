// The Backlog view's runner bar (backlog proposal, ui.md "Runner bar"): the
// runner switch, the limit on runs at once, plan use of the window closest to
// the limit, the runner status with the reason it waits and its main action,
// Run next, and the ⋯ settings. Its
// controls live across renders so a backlog.spend event never takes focus or
// a half-typed value.

import type { BudgetUpdate } from "./backlog_api.js";
import { type BacklogView, type Budget, formatID } from "./backlog_model.js";
import { type Usage, WINDOW_LABELS, pct, tighterWindow, usageHover, usageLines, usd, webReason } from "./backlog_usage.js";
import { locked, paintBusy } from "./backlog_busy.js";
import { h } from "./dom.js";

export interface RunnerActions {
  setRunner(on: boolean): void;
  setMaxRunning(n: number): void;
  setOnDone(onDone: string): void;
  setBudget(update: BudgetUpdate): void;
  runNext(): void;
  openItem(id: number): void;
}

const MAX_RUNNING = 8;

export interface RunnerWords {
  label: string;
  status: string;
  reason: string;
  warn: boolean;
}

const BLOCKED: Record<string, string> = { stopped: "Blocked", waiting: "Waiting" };

/** The switch says only what the user set; the status says what the runner
 *  is doing, and why it is not when a gate is closed. */
export function runnerWords(view: BacklogView): RunnerWords {
  const r = view.runner;
  const reason = webReason(r.reason ?? "").replace(/\.$/, "");
  const label = view.backlog.settings.runner ? "Runner on" : "Runner off";
  const running = r.used > 0 ? `${r.used} running` : "";
  if (!view.backlog.settings.runner) return { label, status: running, reason, warn: false };
  const blocked = BLOCKED[r.state];
  if (blocked) return { label, status: running ? `${running} · ${blocked}` : blocked, reason, warn: true };
  if (r.state === "running") return { label, status: `${r.used} running`, reason: "", warn: false };
  return { label, status: reason || "Idle", reason: "", warn: false };
}

/** The item a paused runner's main action opens: the first unmeterable run
 *  of this project. */
function unmeterableItem(view: BacklogView, usage: Usage | null): number | null {
  const sessions = Object.keys(usage?.unmeterable ?? {});
  for (const it of view.backlog.items) {
    if ((it.runs ?? []).some((r) => sessions.includes(r.session_id))) return it.id;
  }
  const m = /\bB-(\d+)\b/.exec(view.runner.reason ?? "");
  return m ? Number(m[1]) : null;
}

/** The paused runner's main action: the limit gate changes the limit, an
 *  unmeterable run opens its item, no reading runs the next item. */
export type MainAction = { kind: "limit" } | { kind: "open"; id: number } | { kind: "run" } | null;

export function mainAction(view: BacklogView, usage: Usage | null): MainAction {
  if (view.runner.state !== "stopped" && view.runner.state !== "waiting") return null;
  switch (view.runner.gate) {
    case "limit":
      return { kind: "limit" };
    case "no_reading":
      return { kind: "run" };
    case "unmeterable": {
      const id = unmeterableItem(view, usage);
      return id === null ? null : { kind: "open", id };
    }
  }
  return null;
}

interface BudgetField {
  key: keyof BudgetUpdate;
  label: string;
  unit: string;
  max: number;
  step: number;
}

const BUDGET_FIELDS: readonly BudgetField[] = [
  { key: "limit", label: "Plan limit", unit: "% of either window, any use · new runs start only below it", max: 100, step: 1 },
  { key: "max_per_run", label: "Cap per run", unit: "% of the 5-hour window", max: 100, step: 1 },
  { key: "usd_limit", label: "Dollar limit", unit: "$ per window at API prices · 0 is off", max: 1_000_000, step: 0.01 },
];

export class RunnerBar {
  readonly el: HTMLElement;
  private readonly toggle: HTMLButtonElement;
  private readonly toggleLabel = h("span", { class: "af-backlog-runner-state" });
  private readonly statusWord = h("span", { class: "af-backlog-runner-status-word" });
  private readonly reason = h("span", { class: "af-backlog-reason" });
  private readonly status = h("span", { class: "af-backlog-runner-status" }, this.statusWord, this.reason);
  private readonly slots: HTMLSelectElement;
  private readonly plan = h("button", { type: "button", class: "af-backlog-plan" });
  private readonly planDetail = h("div", { class: "af-backlog-plan-detail", hidden: true });
  private slotsKey = "";
  private statusKey = "";
  private readonly main: HTMLButtonElement;
  private readonly run: HTMLButtonElement;
  private readonly menu: HTMLDetailsElement;
  private readonly inputs = new Map<keyof BudgetUpdate, HTMLInputElement>();
  private readonly onDone: HTMLSelectElement;
  private view: BacklogView | null = null;
  private budget: Budget | null = null;
  private usage: Usage | null = null;
  private action: MainAction = null;

  constructor(private readonly actions: RunnerActions, private readonly now: () => number = Date.now) {
    this.toggle = h("button", { type: "button", class: "af-backlog-toggle" },
      h("span", { class: "af-backlog-toggle-knob", ariaHidden: "true" }), this.toggleLabel);
    this.toggle.setAttribute("role", "switch");
    this.toggle.setAttribute("aria-label", "Runner");
    this.toggle.dataset.busy = "runner";
    this.toggle.addEventListener("click", () => {
      if (this.view && !locked(this.toggle)) actions.setRunner(!this.view.backlog.settings.runner);
    });
    this.status.setAttribute("role", "status");
    this.slots = h("select", { class: "af-backlog-slots" });
    this.slots.setAttribute("aria-label", "Max running");
    this.slots.title = "Most items the runner runs at once";
    this.slots.dataset.busy = "slots";
    this.slots.addEventListener("change", () => {
      if (locked(this.slots)) this.slots.value = String(Math.max(1, this.view?.backlog.settings.max_running ?? 1));
      else actions.setMaxRunning(Number(this.slots.value));
    });
    const hover = (): void => { if (this.usage && this.budget) this.plan.title = usageHover(this.usage, this.budget, this.now()); };
    this.plan.addEventListener("pointerenter", hover);
    this.plan.setAttribute("aria-expanded", "false");
    this.plan.addEventListener("click", () => {
      this.planDetail.hidden = !this.planDetail.hidden;
      this.plan.setAttribute("aria-expanded", String(!this.planDetail.hidden));
      this.fillDetail();
    });
    this.main = h("button", { type: "button", class: "af-primary", hidden: true });
    this.main.addEventListener("click", () => this.runMain());
    this.run = h("button", { type: "button", class: "af-ghost" }, "Run next");
    this.run.addEventListener("click", () => actions.runNext());
    this.onDone = h("select", { class: "af-backlog-move-select" },
      h("option", { value: "keep" }, "Keep session"), h("option", { value: "archive" }, "Archive session"));
    this.onDone.setAttribute("aria-label", "When an item is done");
    this.onDone.dataset.busy = "on-done";
    this.onDone.addEventListener("change", () => {
      if (locked(this.onDone)) this.fill(true);
      else actions.setOnDone(this.onDone.value);
    });
    this.menu = h("details", { class: "af-backlog-settings" }, this.menuSummary(), this.menuPanel());
    this.el = h("div", { class: "af-backlog-head" },
      h("div", { class: "af-backlog-runner" }, this.toggle, this.slots, this.plan),
      h("div", { class: "af-backlog-runner-actions" }, this.status, this.main, this.run, this.menu), this.planDetail);
  }

  private menuSummary(): HTMLElement {
    const s = h("summary", { class: "af-ghost af-backlog-more" }, "⋯");
    s.setAttribute("aria-label", "Runner and budget settings");
    return s;
  }

  private menuPanel(): HTMLElement {
    const fields = BUDGET_FIELDS.map((f) => {
      const input = h("input", { type: "number", class: "af-backlog-number", min: "0", max: String(f.max), step: String(f.step) });
      input.setAttribute("aria-label", f.label);
      input.dataset.busyBudget = "budget";
      input.addEventListener("change", () => {
        const v = Number(input.value);
        if (locked(input)) {
          input.value = String(this.budget?.[f.key] ?? "");
          return;
        }
        if (input.value.trim() === "" || !Number.isFinite(v) || v < 0 || v > f.max) {
          this.fill();
          return;
        }
        if (this.budget && this.budget[f.key] === v) return;
        this.actions.setBudget({ [f.key]: v });
      });
      this.inputs.set(f.key, input);
      return h("label", { class: "af-backlog-setting" },
        h("span", { class: "af-backlog-label" }, f.label), input, h("span", { class: "af-backlog-reason" }, f.unit));
    });
    return h("div", { class: "af-backlog-settings-panel" },
      h("p", { class: "af-backlog-reason" }, "Budget · all projects"), ...fields,
      h("p", { class: "af-backlog-reason" }, "Runner · this project"),
      h("label", { class: "af-backlog-setting" }, h("span", { class: "af-backlog-label" }, "When an item is done"), this.onDone));
  }

  private runMain(): void {
    const a = this.action;
    if (!a) return;
    if (a.kind === "run") this.actions.runNext();
    else if (a.kind === "open") this.actions.openItem(a.id);
    else {
      this.menu.open = true;
      this.inputs.get("limit")?.focus();
    }
  }

  /** `busy` is Run next in flight; `held` is what started the request that holds the project's settings, `heldBudget` the one that holds the budget. */
  update(view: BacklogView, budget: Budget | null, usage: Usage | null, busy: boolean, held?: string, heldBudget?: string): void {
    this.view = view;
    this.budget = budget;
    this.usage = usage;
    const words = runnerWords(view);
    const on = view.backlog.settings.runner;
    this.toggle.setAttribute("aria-checked", String(on));
    this.toggle.classList.toggle("af-backlog-toggle-on", on);
    this.toggleLabel.textContent = words.label;
    this.fillStatus(words);
    this.fillSlots(view);
    this.fillPlan();
    this.action = mainAction(view, usage);
    const a = this.action;
    this.main.hidden = a === null || a.kind === "run";
    this.main.textContent = a?.kind === "open" ? `Open ${formatID(a.id)}` : "Change limit";
    this.run.className = a?.kind === "run" ? "af-primary" : "af-ghost";
    this.run.disabled = busy;
    if (busy) this.run.setAttribute("aria-busy", "true");
    else this.run.removeAttribute("aria-busy");
    this.fill();
    paintBusy(this.el, held);
    paintBusy(this.el, heldBudget, "busy-budget");
  }

  /** Writes the live region only when its words change, so a spend event
   *  does not make a screen reader repeat it. */
  private fillStatus(words: RunnerWords): void {
    const reason = words.status && words.reason ? `· ${words.reason}` : words.reason;
    const key = `${words.status}\n${reason}`;
    this.status.classList.toggle("af-backlog-warn", words.warn);
    if (key === this.statusKey) return;
    this.statusKey = key;
    this.statusWord.textContent = words.status;
    this.statusWord.hidden = words.status === "";
    this.reason.textContent = reason;
  }

  /** Rebuilds the options only when their words change: replacing them
   *  closes a native select the user has open. */
  private fillSlots(view: BacklogView): void {
    const max = Math.max(1, view.backlog.settings.max_running);
    const key = String(max);
    if (key === this.slotsKey) return;
    this.slotsKey = key;
    const top = Math.max(MAX_RUNNING, max);
    const opts = [];
    for (let n = 1; n <= top; n++) opts.push(h("option", { value: String(n) }, `Up to ${n} at once`));
    this.slots.replaceChildren(...opts);
    this.slots.value = String(max);
  }

  private fillDetail(): void {
    if (this.planDetail.hidden || !this.usage) return;
    if (!this.budget) return;
    this.planDetail.replaceChildren(...usageLines(this.usage, this.budget, this.now()).map((l) => h("div", {}, l)));
  }

  private fillPlan(): void {
    const u = this.usage;
    const b = this.budget;
    this.plan.hidden = !u || !b;
    if (!u || !b) {
      this.planDetail.hidden = true;
      return;
    }
    this.plan.title = usageHover(u, b, this.now());
    this.fillDetail();
    if (u.metering) {
      this.plan.replaceChildren("Reading plan usage…");
      return;
    }
    const k = tighterWindow(u, this.now());
    if (k === null) {
      this.plan.replaceChildren(b.usd_limit > 0 ? `Backlog limit ${usd(b.usd_limit)} per window` : `Plan limit ${pct(b.limit)}`);
      return;
    }
    const w = u[k];
    const fill = h("span", { class: "af-backlog-meter-fill" });
    fill.style.width = `${b.limit > 0 ? Math.min(100, (w.plan / b.limit) * 100) : 100}%`;
    const meter = h("span", { class: "af-backlog-meter", role: "img" }, fill);
    meter.setAttribute("aria-label", `${pct(w.plan)} of a ${pct(b.limit)} limit`);
    this.plan.replaceChildren(`Plan ${pct(w.plan)} of ${WINDOW_LABELS[k].bar} `, meter, ` limit ${pct(b.limit)}`);
  }

  private fill(force = false): void {
    for (const [key, input] of this.inputs) {
      if (document.activeElement !== input && this.budget) input.value = String(this.budget[key]);
    }
    if (this.view && (force || document.activeElement !== this.onDone)) this.onDone.value = this.view.backlog.settings.on_done || "keep";
  }
}
