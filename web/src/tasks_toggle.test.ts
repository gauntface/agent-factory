import { afterEach, beforeEach, test } from "node:test";
import assert from "node:assert/strict";

import { type FakeElement, installFakeDom } from "./backlog_fakedom.js";
import { TasksPane } from "./tasks.js";
import type { TaskData } from "./types.js";

let dom: ReturnType<typeof installFakeDom>;
beforeEach(() => {
  dom = installFakeDom();
  (dom.doc as unknown as { removeEventListener(): void }).removeEventListener = () => {};
});
afterEach(() => dom.restore());

const task = (enabled: boolean): TaskData =>
  ({ id: "t1", name: "nightly", project_path: "/p", enabled, cron_expr: "0 3 * * *" }) as TaskData;

function mount(enabled: boolean) {
  let settle: (r: boolean | Error) => void = () => {};
  let calls = 0;
  const pane = new TasksPane({
    add() {},
    edit() {},
    trigger() {},
    remove() {},
    toggle: () => {
      calls++;
      return new Promise<boolean>((res, rej) => {
        settle = (r) => (r instanceof Error ? rej(r) : res(r));
      });
    },
  });
  pane.update([task(enabled)], "/p");
  const sw = (): FakeElement => pane.el.querySelector(".af-task-switch") as unknown as FakeElement;
  return { sw, settle: (r: boolean | Error) => settle(r), calls: () => calls, pane };
}

const flush = () => new Promise<void>((r) => setImmediate(r));

test("click shows the new value and stays busy until the request settles", async () => {
  const m = mount(true);
  m.sw().click();
  assert.equal(m.sw().getAttribute("aria-checked"), "false");
  assert.equal(m.sw().getAttribute("aria-disabled"), "true");
  assert.equal(m.sw().getAttribute("aria-busy"), "true");
  m.sw().click();
  assert.equal(m.calls(), 1);
  m.settle(true);
  await flush();
  assert.equal(m.sw().getAttribute("aria-checked"), "false");
  assert.equal(m.sw().getAttribute("aria-disabled"), null);
  assert.equal(m.sw().getAttribute("aria-busy"), null);
});

test("failure restores the old value", async () => {
  for (const fail of [false, new Error("boom")]) {
    const m = mount(true);
    m.sw().click();
    m.settle(fail);
    await flush();
    assert.equal(m.sw().getAttribute("aria-checked"), "true");
    assert.equal(m.sw().getAttribute("aria-disabled"), null);
  }
});

test("a rebuild mid-request keeps the pending state", () => {
  const m = mount(false);
  m.sw().click();
  m.pane.update([task(false)], "/p");
  assert.equal(m.sw().getAttribute("aria-checked"), "true");
  assert.equal(m.sw().getAttribute("aria-disabled"), "true");
  m.sw().click();
  assert.equal(m.calls(), 1);
});
