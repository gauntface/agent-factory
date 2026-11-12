// The config view's Budget section: the budget_limit row, then each Claude
// account's plan use and whether budget-gated tasks run now.

import { type AccountBudget, type Budget, accountLabel, gateLine, pct, planMeter, usageLines } from "./budget.js";
import { h } from "./dom.js";

/** budget is null when the daemon has not answered or has no GetBudget route;
 *  the section then shows only the limit row. */
export function renderBudgetSection(budget: Budget | null, limitRow: HTMLElement | null, now: number): HTMLElement {
  const section = h("div", { class: "af-budget" });
  if (limitRow) section.append(limitRow);
  if (budget) {
    const named = budget.accounts.length > 1;
    for (const a of budget.accounts) section.append(renderAccountBudget(a, budget.limit, now, named));
  }
  return section;
}

function renderAccountBudget(a: AccountBudget, limit: number, now: number, named: boolean): HTMLElement {
  const el = h("div", { class: "af-budget-account" });
  el.setAttribute("data-account", a.account);
  if (named) el.append(h("span", { class: "af-budget-account-name" }, accountLabel(a)));
  const meter = planMeter(a, limit, now);
  const plan = h("div", { class: "af-budget-plan" });
  if (meter) {
    const fill = h("span", { class: "af-budget-meter-fill" });
    fill.style.width = `${meter.fill}%`;
    const bar = h("span", { class: "af-budget-meter", role: "img" }, fill);
    bar.setAttribute("aria-label", meter.ariaLabel);
    plan.append(meter.before, bar, meter.after);
  } else {
    plan.append(`Plan limit ${pct(limit)}`);
  }
  el.append(
    plan,
    h("div", { class: "af-budget-detail" }, ...usageLines(a, limit, now).map((l) => h("div", {}, l))),
    h("div", { class: a.gate.name === "" ? "af-budget-gate" : "af-budget-gate af-budget-warn" }, gateLine(a)),
  );
  return el;
}
