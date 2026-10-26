import { afterEach, beforeEach, mock, test } from "node:test";
import assert from "node:assert/strict";

import { type FakeDocument, type FakeElement, FakeEvent, installFakeDom } from "./backlog_fakedom.js";
import { bodyEditorLoader } from "./backlog_body.js";
import type { BacklogData } from "./backlog_data.js";
import { groupCounts, overview, positions, progressText, segments } from "./backlog_cards.js";
import { type BacklogView, type Item, listRows } from "./backlog_model.js";
import { BacklogPane, type BacklogHost } from "./backlog_pane.js";

let dom: ReturnType<typeof installFakeDom>;
let doc: FakeDocument;
let calls: { method: string; body: Record<string, unknown> }[];
const realFetch = globalThis.fetch;
const realLoad = bodyEditorLoader.load;
const NOW = Date.parse("2000-01-03T10:00:00Z");

beforeEach(() => {
  dom = installFakeDom();
  doc = dom.doc;
  calls = [];
  (globalThis as { fetch: unknown }).fetch = async (url: string, init: RequestInit) => {
    calls.push({ method: url.replace("/v1/", ""), body: JSON.parse(String(init.body)) as Record<string, unknown> });
    return new Response(JSON.stringify({ data: {}, error: null }), { status: 200 });
  };
  bodyEditorLoader.load = async () => ({
    createDocsEditor: (parent: HTMLElement) => {
      (parent as unknown as FakeElement).append(doc.createElement("div"));
      return { getText: () => "", setText: () => {}, destroy: () => {}, applyRemote: () => {}, goToNextQuestion: () => false, focus: () => {} } as never;
    },
  });
  mock.timers.enable({ apis: ["setTimeout", "Date"], now: NOW });
});

afterEach(async () => {
  await settle();
  mock.timers.reset();
  dom.restore();
  (globalThis as { fetch: unknown }).fetch = realFetch;
  bodyEditorLoader.load = realLoad;
});

const settle = async (): Promise<void> => {
  for (let i = 0; i < 5; i++) await new Promise((r) => setImmediate(r));
};

function item(id: number, over: Partial<Item> = {}): Item {
  return { id, title: `item ${id}`, status: "draft", created_at: "", updated_at: "", runs: [], waits_on: [], thread: [], ...over };
}

function view(items: Item[], states: Record<number, string> = {}): BacklogView {
  const st: BacklogView["states"] = {};
  for (const [id, s] of Object.entries(states)) st[id] = { state: s as never };
  return {
    backlog: { schema_version: 2, next_id: 100, settings: { runner: false, max_running: 2, on_done: "keep" }, items },
    states: st, runner: { state: "off", used: 0, max: 2 },
  };
}

function data(v: BacklogView): BacklogData {
  return { views: new Map([["/a", v]]), errors: new Map(), budget: null, usage: null, generation: 1 };
}

function host(): BacklogHost & { notes: string[] } {
  const h = {
    notes: [] as string[],
    token: () => "t",
    refresh: async () => {},
    started: () => 1,
    notify: (m: string) => { h.notes.push(m); },
    openSession: () => {},
    archiveSession: () => {},
    restoreSession: () => {},
    confirm: async () => true,
  };
  return h;
}

function mount(h: BacklogHost = host()): BacklogPane {
  const pane = new BacklogPane(h);
  doc.body.append(pane.el as unknown as FakeElement);
  return pane;
}

const el = (pane: BacklogPane): FakeElement => pane.el as unknown as FakeElement;
const buttonNamed = (root: FakeElement, name: string): FakeElement | undefined =>
  root.querySelectorAll("button").find((b) => b.textContent === name && !b.hidden);
const rowIDs = (pane: BacklogPane): number[] =>
  el(pane).querySelectorAll(".af-backlog-row").map((r) => Number(r.dataset.itemId));

const card = (pane: BacklogPane, id: number): FakeElement =>
  el(pane).querySelector(`.af-backlog-row[data-item-id="${id}"]`)!.closest(".af-backlog-group")!;
const lit = (pane: BacklogPane, cls: string): number[] =>
  el(pane).querySelectorAll(`.af-backlog-row.${cls}`).map((r) => Number(r.dataset.itemId));

// 1 { 2, 3 { 4 }, 5 }, 6, 7 { 8 }
const tree = (): Item[] => [
  item(1), item(2, { parent: 1, status: "done" }), item(3, { parent: 1 }), item(4, { parent: 3, waits_on: [2, 9] }),
  item(5, { parent: 1, waits_on: [4, 2] }), item(6), item(7, { paused: true }), item(8, { parent: 7 }), item(9),
];
const states = { 2: "done", 4: "working", 5: "needs_you", 8: "queued" };

test("segments split groups from top-level items and keep tree order", () => {
  const v = view(tree(), states);
  const segs = segments(listRows(v, { filter: "all", showDone: true, folded: new Set() }));
  assert.deepEqual(segs.map((s) => s.kind === "group" ? [s.head.item.id, s.rows.map((r) => r.item.id)] : s.rows.map((r) => r.item.id)),
    [[1, [2, 3, 4, 5]], [6], [7, [8]], [9]]);
  assert.deepEqual([...positions(v.backlog, 1)], [[2, 1], [3, 2], [4, 3], [5, 4]]);
});

test("group counts take leaves only, and the overview leaves out archived groups", () => {
  const v = view([...tree(), item(10, { archived: true, paused: true }), item(11, { parent: 10 })], states);
  assert.deepEqual(groupCounts(v, 1), { done: 1, total: 3, working: 1, needsYou: 1 });
  assert.equal(progressText(groupCounts(v, 1)), "1 of 3 done · 1 working · 1 needs you");
  assert.equal(progressText(groupCounts(v, 7)), "0 of 1 done");
  assert.equal(overview(v), "2 groups · 6 items · 1 working · 1 needs you");
});

test("a group is a card: header, progress, rows with place and waits, and an add row", () => {
  const pane = mount();
  pane.update(data(view(tree(), states)), "/a", []);
  const c = card(pane, 1);
  assert.deepEqual(c.querySelectorAll(".af-backlog-row").map((r) => Number(r.dataset.itemId)), [1, 3, 4, 5]);
  const head = c.querySelector(".af-backlog-group-head")!;
  assert.equal(Number(head.dataset.itemId), 1);
  assert.match(head.textContent, /item 1B-11 needs youPauseDetails/);
  assert.equal(c.querySelector(".af-backlog-progress")!.textContent, "1 of 3 done · 1 working · 1 needs you");
  const row5 = c.querySelector('.af-backlog-row[data-item-id="5"]')!;
  assert.equal(row5.querySelector(".af-backlog-pos")!.textContent, "#4");
  assert.equal(row5.querySelector(".af-backlog-meta")!.textContent, "B-5·after#1#3·waiting on 1 item");
  assert.equal(c.querySelector('.af-backlog-row[data-item-id="4"] .af-backlog-meta')!.textContent, "B-4·after#1·waiting on 1 item");
  assert.ok(c.querySelector(".af-backlog-group-add input"));
  assert.equal(c.querySelector(".af-backlog-notice"), null);
  assert.equal(el(pane).querySelector('.af-backlog-row[data-item-id="6"]')!.closest(".af-backlog-group"), null);
  assert.equal(el(pane).querySelector(".af-backlog-overview")!.textContent, "2 groups · 6 items · 1 working · 1 needs you");
});

test("a paused group shows a notice and Unpause; a folded group hides its rows and add row", async () => {
  const pane = mount();
  pane.update(data(view(tree(), states)), "/a", []);
  const c = card(pane, 7);
  assert.match(c.querySelector(".af-backlog-notice")!.textContent, /^‖ Paused — the runner starts nothing/);
  buttonNamed(c, "Unpause")!.click();
  await settle();
  assert.deepEqual(calls.at(-1), { method: "UpdateBacklogItem", body: { repo_path: "/a", id: 7, paused: false } });
  assert.equal(el(pane).querySelector(".af-backlog-panel"), null);
  c.querySelector(".af-backlog-fold")!.click();
  const folded = card(pane, 7);
  assert.deepEqual(folded.querySelectorAll(".af-backlog-row").map((r) => Number(r.dataset.itemId)), [7]);
  assert.equal(folded.querySelector(".af-backlog-group-add"), null);
  buttonNamed(folded, "Details")!.click();
  assert.ok(el(pane).querySelector(".af-backlog-panel"));
});

test("Add item makes a child of the group and keeps focus across a rerender; New group makes a top-level item", async () => {
  const pane = mount();
  pane.update(data(view(tree(), states)), "/a", []);
  const input = card(pane, 1).querySelector(".af-backlog-group-add input")!;
  input.focus();
  input.value = "child";
  input.dispatchEvent(new FakeEvent("keydown", { key: "Enter" }));
  await settle();
  assert.deepEqual(calls.at(-1), { method: "AddBacklogItems", body: { repo_path: "/a", items: [{ key: "new", title: "child", parent: 1 }] } });
  input.value = "half typed";
  pane.update(data(view([...tree(), item(12, { parent: 1 })], states)), "/a", []);
  assert.equal(card(pane, 1).querySelector(".af-backlog-group-add input"), input);
  assert.equal(doc.activeElement, input);
  assert.equal(input.value, "half typed");
  const group = el(pane).querySelector('input[aria-label="New group title"]')!;
  group.value = "Docs refresh";
  group.dispatchEvent(new FakeEvent("keydown", { key: "Enter" }));
  await settle();
  assert.deepEqual(calls.at(-1), { method: "AddBacklogItems", body: { repo_path: "/a", items: [{ key: "new", title: "Docs refresh" }] } });
});

test("a selected row lights up what it waits on and what waits on it, without dimming the rest", () => {
  const pane = mount();
  pane.update(data(view(tree(), states)), "/a", []);
  pane.select(4);
  assert.deepEqual(lit(pane, "af-backlog-row-related"), [5, 9]);
  assert.equal(el(pane).querySelector(".af-backlog-dim"), null);
});
