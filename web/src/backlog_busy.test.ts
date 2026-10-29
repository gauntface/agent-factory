import { afterEach, beforeEach, mock, test } from "node:test";
import assert from "node:assert/strict";

import { type FakeDocument, type FakeElement, FakeEvent, installFakeDom } from "./backlog_fakedom.js";
import { bodyEditorLoader } from "./backlog_body.js";
import { Busy, itemScope, locked as lockedEl, paintBusy } from "./backlog_busy.js";
import type { BacklogData } from "./backlog_data.js";
import type { BacklogView, Item } from "./backlog_model.js";
import { BacklogLoader } from "./backlog_data.js";
import { BacklogPane, type BacklogHost } from "./backlog_pane.js";

interface Call {
  method: string;
  body: Record<string, unknown>;
}

interface Gate {
  promise: Promise<unknown>;
  resolve(v?: unknown): void;
  reject(message: string): void;
}

function gate(): Gate {
  let resolve!: (v?: unknown) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<unknown>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve: (v = {}) => resolve(v), reject: (m) => reject(new Error(m)) };
}

let dom: ReturnType<typeof installFakeDom>;
let doc: FakeDocument;
let calls: Call[];
let reply: (method: string) => Promise<unknown>;
const realFetch = globalThis.fetch;
const realLoad = bodyEditorLoader.load;

beforeEach(() => {
  dom = installFakeDom();
  doc = dom.doc;
  calls = [];
  reply = async () => ({});
  (globalThis as { fetch: unknown }).fetch = async (url: string, init: RequestInit) => {
    const method = url.replace("/v1/", "");
    calls.push({ method, body: JSON.parse(String(init.body)) });
    try {
      return new Response(JSON.stringify({ data: await reply(method), error: null }), { status: 200 });
    } catch (e) {
      return new Response(JSON.stringify({ data: null, error: { message: (e as Error).message } }), { status: 400 });
    }
  };
  bodyEditorLoader.load = async () => ({
    createDocsEditor: (parent: HTMLElement) => {
      (parent as unknown as FakeElement).append(doc.createElement("div"));
      return { getText: () => "", setText: () => {}, destroy: () => {}, applyRemote: () => {}, goToNextQuestion: () => false, focus: () => {} } as never;
    },
  });
  mock.timers.enable({ apis: ["setTimeout", "Date"], now: Date.parse("2000-01-03T10:00:00") });
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
    backlog: { schema_version: 1, next_id: 100, settings: { runner: false, max_running: 2, on_done: "keep" }, items },
    states: st, runner: { state: "off", reason: "", used: 0, max: 2 },
  };
}

function data(v: BacklogView): BacklogData {
  return { views: new Map([["/a", v]]), errors: new Map(), budget: null, usage: null, generation: 1 };
}

interface TestHost extends BacklogHost {
  notices: string[];
  refreshes: number;
}

function host(): TestHost {
  const h: TestHost = {
    notices: [],
    refreshes: 0,
    token: () => "t",
    refresh: async () => { h.refreshes++; },
    started: () => h.refreshes,
    notify: (m: string) => { h.notices.push(m); },
    openSession: () => {},
    archiveSession: () => {},
    restoreSession: () => {},
    confirm: async () => true,
  };
  return h;
}

function mount(h: BacklogHost, v: BacklogView): BacklogPane {
  const pane = new BacklogPane(h);
  doc.body.append(pane.el as unknown as FakeElement);
  pane.update(data(v), "/a", []);
  pane.select(1);
  return pane;
}

const el = (pane: BacklogPane): FakeElement => pane.el as unknown as FakeElement;
const named = (pane: BacklogPane, name: string): FakeElement =>
  el(pane).querySelectorAll("button").find((b) => b.textContent === name && !b.hidden)!;
const action = (pane: BacklogPane, key: string): FakeElement => el(pane).querySelector(`.af-backlog-panel [data-action="${key}"]`)!;
const sent = (method: string): Call[] => calls.filter((c) => c.method === method);
const locked = (b: FakeElement): boolean => lockedEl(b as unknown as Element);
const busy = (b: FakeElement): boolean => b.getAttribute("aria-busy") === "true";

test("Busy refuses a second request for a held scope and frees it on success and failure", async () => {
  let changes = 0;
  const b = new Busy(() => { changes++; });
  const first = gate();
  const ran = b.run("s", "a", () => first.promise);
  assert.equal(b.by("s"), "a");
  assert.equal(await b.run("s", "b", async () => "second"), undefined);
  first.resolve();
  await ran;
  assert.equal(b.by("s"), undefined);
  await assert.rejects(b.run("s", "a", async () => { throw new Error("no"); }));
  assert.equal(b.by("s"), undefined);
  assert.equal(changes, 4);
  assert.equal(itemScope("/a", 1), "item\n/a\n1");
});

test("paintBusy marks every control and the starter busy, and clears them", () => {
  const root = doc.createElement("div");
  const a = doc.createElement("button");
  a.dataset.busy = "one";
  const b = doc.createElement("button");
  b.dataset.busy = "two";
  root.append(a, b);
  paintBusy(root as unknown as ParentNode, "one");
  assert.deepEqual([locked(a), busy(a), locked(b), busy(b)], [true, true, true, false]);
  paintBusy(root as unknown as ParentNode, undefined);
  assert.deepEqual([locked(a), busy(a), locked(b), busy(b)], [false, false, false, false]);
});

test("a second click on an item action sends nothing; every action shows busy and the item frees on success", async () => {
  const h = host();
  const pane = mount(h, view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  const queue = action(pane, "queue");
  queue.click();
  queue.click();
  await settle();
  assert.equal(sent("SetBacklogItemStatus").length, 1);
  assert.ok(busy(action(pane, "queue")));
  assert.ok(locked(action(pane, "delete")));
  assert.ok(!busy(action(pane, "delete")));
  action(pane, "delete").click();
  action(pane, "queue").click();
  await settle();
  assert.equal(calls.length, 1, "no other request for the item while one is out");
  g.resolve();
  await settle();
  assert.ok(!locked(action(pane, "queue")) && !busy(action(pane, "queue")));
  assert.ok(!locked(action(pane, "delete")));
  action(pane, "delete").click();
  await settle();
  assert.equal(sent("DeleteBacklogItem").length, 1);
});

test("an item action frees after a failure, and says why", async () => {
  const h = host();
  const pane = mount(h, view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  action(pane, "queue").click();
  await settle();
  assert.ok(locked(action(pane, "queue")));
  g.reject("B-1 not found");
  await settle();
  assert.deepEqual(h.notices, ["B-1 not found"]);
  assert.ok(!locked(action(pane, "queue")) && !busy(action(pane, "queue")));
  reply = async () => ({});
  action(pane, "queue").click();
  await settle();
  assert.equal(sent("SetBacklogItemStatus").length, 2);
});

test("Delete twice, even on the stale button, sends one request", async () => {
  const pane = mount(host(), view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  const stale = action(pane, "delete");
  stale.click();
  stale.click();
  await settle();
  assert.equal(sent("DeleteBacklogItem").length, 1);
  g.resolve();
});

test("a held item keeps its busy state across the rerender a refresh brings", async () => {
  const pane = mount(host(), view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  action(pane, "queue").click();
  await settle();
  action(pane, "queue").focus();
  pane.update(data(view([item(1, { title: "renamed" })], { 1: "draft" })), "/a", []);
  assert.ok(locked(action(pane, "queue")) && busy(action(pane, "queue")));
  assert.ok(locked(named(pane, "Add note")));
  assert.equal(doc.activeElement, action(pane, "queue"));
  g.resolve();
  await settle();
  assert.ok(!locked(action(pane, "queue")));
});

test("group buttons, the move menu and Add note hold the item too", async () => {
  const pane = mount(host(), view([item(1), item(2, { parent: 1 })], { 1: "draft", 2: "draft" }));
  const g = gate();
  reply = () => g.promise;
  action(pane, "pause").click();
  action(pane, "pause").click();
  await settle();
  assert.equal(sent("UpdateBacklogItem").length, 1);
  assert.ok(busy(action(pane, "pause")));
  assert.ok(locked(named(pane, "Top")) && locked(named(pane, "Bottom")));
  assert.ok(locked(action(pane, "archive")));
  const select = el(pane).querySelector('select[aria-label="Above…"]')!;
  assert.ok(locked(select));
  const note = el(pane).querySelector("textarea.af-backlog-note")!;
  note.value = "hello";
  named(pane, "Add note").click();
  assert.equal(note.value, "hello", "a refused note stays in the box");
  named(pane, "Top").click();
  await settle();
  assert.equal(calls.length, 1);
  g.resolve();
  await settle();
  assert.ok(!locked(named(pane, "Top")));
});

test("the move menu sends one move at a time and frees after it fails", async () => {
  const h = host();
  const pane = mount(h, view([item(1), item(2)], { 1: "draft", 2: "draft" }));
  const g = gate();
  reply = () => g.promise;
  named(pane, "Top").click();
  await settle();
  assert.ok(busy(named(pane, "Top")));
  named(pane, "Bottom").click();
  named(pane, "Top").click();
  await settle();
  assert.equal(sent("MoveBacklogItem").length, 1);
  g.reject("refused");
  await settle();
  assert.deepEqual(h.notices, ["refused"]);
  assert.ok(!locked(named(pane, "Top")));
});

test("Add child ignores Enter while a request is out and keeps the text", async () => {
  const pane = mount(host(), view([item(1), item(2, { parent: 1 })], { 1: "draft", 2: "draft" }));
  const g = gate();
  reply = () => g.promise;
  const entry = (): FakeElement => el(pane).querySelector('.af-backlog-panel input[aria-label="Add a child to B-1"]')!;
  const enter = (text: string): void => {
    entry().value = text;
    entry().dispatchEvent(new FakeEvent("keydown", { key: "Enter" }));
  };
  enter("first");
  await settle();
  assert.equal(entry().value, "");
  assert.ok(locked(entry()));
  enter("second");
  await settle();
  assert.equal(sent("AddBacklogItems").length, 1);
  assert.equal(entry().value, "second");
  g.resolve();
  await settle();
  assert.ok(!locked(entry()));
});

test("the runner switch and the new group box hold the project, and free it again", async () => {
  const h = host();
  const pane = mount(h, view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  const sw = (): FakeElement => el(pane).querySelector('[role="switch"][aria-label="Runner"]')!;
  sw().click();
  sw().click();
  await settle();
  assert.equal(sent("SetBacklogSettings").length, 1);
  assert.ok(busy(sw()));
  const slots = el(pane).querySelector('select[aria-label="Max running"]')!;
  assert.ok(locked(slots) && !busy(slots));
  slots.value = "4";
  slots.dispatchEvent(new FakeEvent("change"));
  assert.equal(slots.value, "2", "a refused change snaps back");
  const group = el(pane).querySelector('input[aria-label="New group title"]')!;
  assert.ok(locked(group));
  g.reject("nope");
  await settle();
  assert.ok(!locked(sw()) && !busy(sw()) && !locked(group));
  assert.deepEqual(h.notices, ["nope"]);
});

test("Send answer holds the item until the daemon replies", async () => {
  const q = { id: 4, kind: "question", author: "agent:s1", text: "Which share?", at: "" };
  const pane = mount(host(), view([item(1, { status: "running", thread: [q] })], { 1: "needs_you" }));
  const g = gate();
  reply = () => g.promise;
  const answer = el(pane).querySelector(".af-backlog-question textarea")!;
  answer.value = "30%";
  named(pane, "Send answer").click();
  named(pane, "Send answer").click();
  await settle();
  assert.equal(sent("PostBacklogMessage").length, 1);
  assert.ok(locked(named(pane, "Add note")));
  g.reject("closed");
  await settle();
  assert.ok(!locked(named(pane, "Add note")));
  assert.equal(answer.value, "30%");
});

test("the item stays held while a superseded refresh resolves, until the newer one commits", async () => {
  const h = host();
  const pane = mount(h, view([item(1)], { 1: "draft" }));
  const fetches: { method: string; gate: Gate }[] = [];
  reply = (method) => {
    if (method === "SetBacklogItemStatus") return Promise.resolve({});
    const g = gate();
    fetches.push({ method, gate: g });
    return g.promise;
  };
  const latest = view([item(1, { status: "queued" })], { 1: "queued" });
  let current = data(view([item(1)], { 1: "draft" }));
  const loader = new BacklogLoader({
    token: () => "t",
    roots: () => ["/a"],
    current: () => current,
    commit: (d) => {
      current = d;
      pane.update(d, "/a", []);
    },
  });
  h.refresh = () => loader.refresh();
  h.started = () => loader.started();
  const reply2 = { backlog: latest.backlog, states: { 1: { state: "queued" } }, runner: latest.runner };
  const answerFetches = (n: number): void => {
    for (const f of fetches.splice(0, n)) f.gate.resolve(f.method === "GetBacklog" ? reply2 : { budget: { limit: 1, max_per_run: 1, usd_limit: 0, usd_spent: 0 } });
  };
  action(pane, "queue").click();
  await settle();
  assert.equal(fetches.length, 2, "refresh A is fetching");
  loader.request();
  mock.timers.tick(150);
  await settle();
  assert.equal(fetches.length, 4, "refresh B started while A was fetching");
  answerFetches(2);
  await settle();
  assert.ok(locked(action(pane, "delete")), "A resolved early, the old state still shows, the item stays held");
  action(pane, "delete").click();
  action(pane, "queue").click();
  await settle();
  assert.equal(calls.filter((c) => c.method !== "GetBacklog" && c.method !== "GetBudget").length, 1);
  answerFetches(2);
  await settle();
  assert.equal(el(pane).querySelectorAll('.af-backlog-panel [aria-disabled="true"]').length, 0, "the newer refresh committed");
});

test("a locked Delete button is aria-disabled and refuses", async () => {
  const pane = mount(host(), view([item(1)], { 1: "draft" }));
  const g = gate();
  reply = () => g.promise;
  action(pane, "queue").click();
  await settle();
  const del = action(pane, "delete");
  assert.equal(del.getAttribute("aria-disabled"), "true");
  del.click();
  await settle();
  assert.equal(sent("DeleteBacklogItem").length, 0);
  g.resolve();
});

test("a refused budget edit puts the stored value back, even in the focused box, and the budget is not the project's scope", async () => {
  const h = host();
  const pane = new BacklogPane(h);
  doc.body.append(pane.el as unknown as FakeElement);
  const d = { ...data(view([item(1)], { 1: "draft" })), budget: { limit: 50, max_per_run: 10, usd_limit: 0, usd_spent: 0 } };
  pane.update(d, "/a", []);
  const g = gate();
  reply = () => g.promise;
  const input = (label: string): FakeElement => el(pane).querySelector(`input[aria-label="${label}"]`)!;
  input("Plan limit").value = "30";
  input("Plan limit").dispatchEvent(new FakeEvent("change"));
  await settle();
  assert.ok(locked(input("Cap per run")));
  assert.ok(!locked(el(pane).querySelector('select[aria-label="Max running"]')!), "settings are the project's, not the budget's");
  input("Cap per run").focus();
  input("Cap per run").value = "20";
  input("Cap per run").dispatchEvent(new FakeEvent("change"));
  assert.equal(input("Cap per run").value, "10");
  assert.equal(sent("SetBudget").length, 1);
  g.resolve();
});

test("filtering the wait picker keeps its options locked while the item is held", async () => {
  const pane = mount(host(), view([item(1), item(2), item(3)], { 1: "draft", 2: "draft", 3: "draft" }));
  named(pane, "Add").click();
  const g = gate();
  reply = () => g.promise;
  action(pane, "queue").click();
  await settle();
  const search = el(pane).querySelector(".af-backlog-picker input")!;
  search.value = "2";
  search.dispatchEvent(new FakeEvent("input"));
  const options = el(pane).querySelectorAll(".af-backlog-option");
  assert.equal(options.length, 1);
  assert.ok(locked(options[0]));
  options[0].click();
  await settle();
  assert.equal(sent("UpdateBacklogItem").length, 0);
  g.resolve();
});

test("Run children in order keeps the user's value while its request is out", async () => {
  const pane = mount(host(), view([item(1), item(2, { parent: 1 })], { 1: "draft", 2: "draft" }));
  const g = gate();
  reply = () => g.promise;
  const sw = (): FakeElement => el(pane).querySelector(".af-backlog-switch input")!;
  assert.equal(sw().checked, false);
  sw().checked = true;
  sw().dispatchEvent(new FakeEvent("change"));
  await settle();
  assert.equal(sent("UpdateBacklogItem").length, 1);
  assert.equal(sw().checked, true);
  assert.ok(locked(sw()));
  g.reject("refused");
  await settle();
  assert.equal(sw().checked, false, "a failed request shows the daemon's value again");
});
