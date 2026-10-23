import { test } from "node:test";
import assert from "node:assert/strict";

import type { BacklogView, Item } from "./backlog_model.js";
import { backlogItemsLabel, backlogItemsPrompt, proposalItems } from "./docs_backlog.js";

function item(id: number, over: Partial<Item> = {}): Item {
  return { id, title: `item ${id}`, status: "draft", created_at: "", updated_at: "", ...over };
}

function view(...items: Item[]): BacklogView {
  return {
    backlog: { schema_version: 1, next_id: 100, settings: { runner: false, max_running: 1, on_done: "keep" }, items },
    states: {},
    runner: { state: "off", used: 0, max: 1 },
  };
}

const from = (slug: string, key: string) => ({ source: { proposal: slug, key } });

test("proposalItems: counts the proposal's items and opens its parent", () => {
  const v = view(
    item(1),
    item(2, from("other", "other")),
    item(3, from("backlog", "backlog")),
    item(4, { parent: 3, ...from("backlog", "pr-1") }),
    item(5, { parent: 3, ...from("backlog", "pr-2") }),
  );
  assert.deepEqual(proposalItems(v, "backlog"), { count: 3, first: 3 });
});

test("proposalItems: a child under a parent from elsewhere is the first item", () => {
  const v = view(item(1), item(2, { parent: 1, ...from("backlog", "pr-1") }));
  assert.deepEqual(proposalItems(v, "backlog"), { count: 1, first: 2 });
});

test("proposalItems: no view or no items", () => {
  assert.deepEqual(proposalItems(undefined, "backlog"), { count: 0, first: 0 });
  assert.deepEqual(proposalItems(view(item(1)), "backlog"), { count: 0, first: 0 });
});

test("the menu item says Make before items exist and Update after", () => {
  assert.equal(backlogItemsLabel({ count: 0, first: 0 }), "Make backlog items");
  assert.equal(backlogItemsLabel({ count: 2, first: 7 }), "Update backlog items");
});

test("the request names the proposal and the batch command, and is not a pushed-edits prompt", () => {
  for (const items of [{ count: 0, first: 0 }, { count: 3, first: 3 }]) {
    const p = backlogItemsPrompt("backlog", items);
    assert.match(p, /proposal backlog\b/);
    assert.match(p, /af backlog add --file -/);
    assert.match(p, /proposal rule for backlog items/);
    assert.ok(!p.startsWith("Proposal backlog:"), p);
  }
  assert.match(backlogItemsPrompt("backlog", { count: 3, first: 3 }), /same keys/);
});
