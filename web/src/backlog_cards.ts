// The Backlog list as group cards (backlog-v2 groups): pure counts and the
// card parts the pane wraps around its rows. A group is a top-level parent;
// its descendants are the card's rows.

import {
  type Backlog, type BacklogView, type Item, type ListRow, type State,
  archivedBy, derivedOf, formatID, isAncestor, isParent, itemByID, needsYouLabel,
} from "./backlog_model.js";
import { PAUSED_GLYPH } from "./backlog_group_panel.js";
import { h } from "./dom.js";

export type Segment =
  | { kind: "group"; head: ListRow; rows: ListRow[] }
  | { kind: "loose"; rows: ListRow[] };

/** Splits the list rows into group cards and runs of top-level items with no
 *  children, keeping tree order. */
export function segments(rows: readonly ListRow[]): Segment[] {
  const out: Segment[] = [];
  for (const row of rows) {
    const last = out.at(-1);
    if (row.depth === 0 && row.parent) out.push({ kind: "group", head: row, rows: [] });
    else if (row.depth > 0 && last?.kind === "group") last.rows.push(row);
    else if (last?.kind === "loose") last.rows.push(row);
    else out.push({ kind: "loose", rows: [row] });
  }
  return out;
}

/** Each descendant's place in its group, counted from 1 in tree order. */
export function positions(b: Backlog, group: number): Map<number, number> {
  const out = new Map<number, number>();
  for (const it of b.items) if (isAncestor(b, group, it.id)) out.set(it.id, out.size + 1);
  return out;
}

export interface Counts {
  done: number;
  total: number;
  working: number;
  needsYou: number;
}

const working = (s: State): boolean => s === "working" || s === "starting";

function count(view: BacklogView, items: Item[]): Counts {
  const c: Counts = { done: 0, total: 0, working: 0, needsYou: 0 };
  for (const it of items) {
    const s = derivedOf(view, it.id).state;
    if (s === "dropped") continue;
    c.total++;
    if (s === "done") c.done++;
    if (working(s)) c.working++;
    if (s === "needs_you") c.needsYou++;
  }
  return c;
}

/** The group's leaf items, without dropped ones. Parents roll their
 *  children up, so counting them too would count work twice. */
export function groupCounts(view: BacklogView, group: number): Counts {
  const b = view.backlog;
  return count(view, b.items.filter((it) => isAncestor(b, group, it.id) && !isParent(b, it.id)));
}

const plural = (n: number, one: string): string => `${n} ${one}${n === 1 ? "" : "s"}`;

function tail(c: Counts): string[] {
  return [...(c.working > 0 ? [`${c.working} working`] : []), ...(c.needsYou > 0 ? [needsYouLabel(c.needsYou)] : [])];
}

export function progressText(c: Counts): string {
  return [`${c.done} of ${c.total} done`, ...tail(c)].join(" · ");
}

/** The page header line: groups, items, working and needs-you, leaving out
 *  archived groups. */
export function overview(view: BacklogView): string {
  const b = view.backlog;
  const live = b.items.filter((it) => !archivedBy(b, it.id));
  const groups = live.filter((it) => !it.parent && isParent(b, it.id)).length;
  const c = count(view, live.filter((it) => !isParent(b, it.id)));
  return [plural(groups, "group"), plural(c.total, "item"), ...tail(c)].join(" · ");
}

/** The open items it waits on, and those of them in the same group by place. */
export function waitsInGroup(b: Backlog, it: Item, pos: ReadonlyMap<number, number>): { open: number; after: number[] } {
  let open = 0;
  const after: number[] = [];
  for (const w of it.waits_on ?? []) {
    const x = itemByID(b, w);
    if (x && x.status !== "done" && x.status !== "dropped") open++;
    const p = pos.get(w);
    if (p !== undefined) after.push(p);
  }
  return { open, after: after.sort((x, y) => x - y) };
}

/** The line under a card row's title: id, the in-group items it comes after,
 *  and how many open items it still waits on. */
export function metaLine(b: Backlog, it: Item, pos: ReadonlyMap<number, number>): HTMLElement {
  const { open, after } = waitsInGroup(b, it, pos);
  const parts: Node[] = [h("span", { class: "af-backlog-id" }, formatID(it.id))];
  if (after.length > 0) {
    parts.push(h("span", { ariaHidden: "true" }, "·"), h("span", {}, "after"),
      ...after.map((p) => h("span", { class: "af-backlog-pos-chip" }, `#${p}`)));
  }
  if (open > 0) parts.push(h("span", { ariaHidden: "true" }, "·"), h("span", {}, `waiting on ${plural(open, "item")}`));
  return h("span", { class: "af-backlog-meta" }, ...parts);
}

export function presentational<T extends HTMLElement>(el: T): T {
  el.setAttribute("role", "none");
  return el;
}

export function progress(c: Counts): HTMLElement {
  const pct = c.total === 0 ? 0 : Math.round((c.done / c.total) * 100);
  const fill = h("span", { class: "af-backlog-progress-fill" });
  fill.style.width = `${pct}%`;
  return presentational(h("li", { class: "af-backlog-progress" },
    h("span", { class: "af-backlog-progress-bar", ariaHidden: "true" }, fill),
    h("span", { class: "af-backlog-reason" }, progressText(c))));
}

export function pausedNotice(): HTMLElement {
  return presentational(h("li", { class: "af-backlog-notice" },
    h("span", { ariaHidden: "true" }, PAUSED_GLYPH), " Paused — the runner starts nothing in this group until you unpause it."));
}
