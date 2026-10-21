import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

import { placeNewDocsTabs } from "./docs_autosplit.js";
import { closeLeaf, type LayoutNode, leaves, resetIds, singleLeaf, splitLeaf } from "./layout.js";
import type { SplitView as SplitViewType } from "./split.js";
import { TabKind } from "./types.js";

register("./browser_stub_loader.mjs", import.meta.url);
const { SplitView } = (await import("./split.js")) as { SplitView: typeof SplitViewType };

const A = TabKind.Agent;
const S = TabKind.Shell;
const D = TabKind.Docs;
const key = (id: string) => id;

function tabs(node: LayoutNode): number[] {
  return leaves(node).map((l) => l.tab);
}

function rowRatio(node: LayoutNode): [string, number] | null {
  return node.kind === "split" ? [node.dir, node.ratio] : null;
}

test("a Docs tab that appears while viewing splits right of Agent, 50/50", () => {
  resetIds();
  const seen = new Set<string>();
  let tree: LayoutNode = singleLeaf(0);
  tree = placeNewDocsTabs(tree, ["agent", "shell"], [A, S], seen, key, false);
  assert.deepEqual(tabs(tree), [0], "no Docs tab: layout untouched");
  tree = placeNewDocsTabs(tree, ["agent", "shell", "docs"], [A, S, D], seen, key, false);
  assert.deepEqual(tabs(tree), [0, 2], "Agent left, Docs right");
  assert.deepEqual(rowRatio(tree), ["row", 0.5]);
});

test("a session opened with an existing Docs tab shows Agent and Docs", () => {
  resetIds();
  const tree = placeNewDocsTabs(singleLeaf(0), ["agent", "docs"], [A, D], new Set(), key, false);
  assert.deepEqual(tabs(tree), [0, 1]);
  assert.deepEqual(rowRatio(tree), ["row", 0.5]);
});

test("a split the user closes stays closed", () => {
  resetIds();
  const seen = new Set<string>();
  const ids = ["agent", "docs"];
  const split = placeNewDocsTabs(singleLeaf(0), ids, [A, D], seen, key, false);
  const docsLeaf = leaves(split)[1]!;
  const closed = closeLeaf(split, docsLeaf.id)!;
  const again = placeNewDocsTabs(closed, ids, [A, D], seen, key, false);
  assert.equal(again, closed, "the same tree comes back");
  assert.deepEqual(tabs(again), [0]);
});

test("a rearranged split is not put back", () => {
  resetIds();
  const seen = new Set<string>();
  const ids = ["agent", "shell", "docs"];
  const kinds = [A, S, D];
  const split = placeNewDocsTabs(singleLeaf(0), ids, kinds, seen, key, false);
  const moved = splitLeaf(split, leaves(split)[0]!.id, "bottom", 2);
  assert.equal(placeNewDocsTabs(moved, ids, kinds, seen, key, false), moved);
});

test("at phone width the Docs tab stays a tab, and is not split later", () => {
  resetIds();
  const seen = new Set<string>();
  const leaf = singleLeaf(0);
  const narrow = placeNewDocsTabs(leaf, ["agent", "docs"], [A, D], seen, key, true);
  assert.equal(narrow, leaf);
  assert.equal(placeNewDocsTabs(leaf, ["agent", "docs"], [A, D], seen, key, false), leaf);
});

test("a second Docs tab replaces the Docs side instead of adding a third pane", () => {
  resetIds();
  const seen = new Set<string>();
  let tree = placeNewDocsTabs(singleLeaf(0), ["agent", "docs1"], [A, D], seen, key, false);
  const docsLeafId = leaves(tree)[1]!.id;
  tree = placeNewDocsTabs(tree, ["agent", "docs1", "docs2"], [A, D, D], seen, key, false);
  assert.deepEqual(tabs(tree), [0, 2]);
  assert.equal(leaves(tree)[1]!.id, docsLeafId, "the same pane now shows the new proposal");
});

test("a second Docs tab after the first split was closed splits right of Agent again", () => {
  resetIds();
  const seen = new Set<string>();
  const first = placeNewDocsTabs(singleLeaf(0), ["agent", "docs1"], [A, D], seen, key, false);
  const closed = closeLeaf(first, leaves(first)[1]!.id)!;
  const tree = placeNewDocsTabs(closed, ["agent", "docs1", "docs2"], [A, D, D], seen, key, false);
  assert.deepEqual(tabs(tree), [0, 2]);
});

test("with no Agent pane, a single pane keeps its tab and gains Docs on the right", () => {
  resetIds();
  const tree = placeNewDocsTabs(singleLeaf(1), ["agent", "shell", "docs"], [A, S, D], new Set(), key, false);
  assert.deepEqual(tabs(tree), [1, 2]);
});

test("a multi-pane layout with no Agent pane is left alone", () => {
  resetIds();
  const base = splitLeaf(singleLeaf(1), "leaf1", "right", 2);
  const ids = ["agent", "s1", "s2", "docs"];
  assert.equal(placeNewDocsTabs(base, ids, [A, S, S, D], new Set(), key, false), base);
});

type Internals = {
  placeDocs: (sessionId: string, tree: LayoutNode, tabIds: string[], tabKinds: number[]) => LayoutNode;
};

function view(narrow: boolean): Internals {
  const noop = { onStatus: () => {}, onFocusChange: () => {}, onLayout: () => {} };
  return new SplitView(null as unknown as HTMLElement, noop, () => narrow) as unknown as Internals;
}

test("SplitView records Docs tabs per session", () => {
  resetIds();
  const v = view(false);
  const ids = ["agent", "docs"];
  assert.deepEqual(tabs(v.placeDocs("s1", singleLeaf(0), ids, [A, D])), [0, 1]);
  assert.deepEqual(tabs(v.placeDocs("s1", singleLeaf(0), ids, [A, D])), [0], "once per tab");
  assert.deepEqual(tabs(v.placeDocs("s2", singleLeaf(0), ids, [A, D])), [0, 1], "other session");
});

test("SplitView reads phone width from its narrow probe", () => {
  resetIds();
  assert.deepEqual(tabs(view(true).placeDocs("s1", singleLeaf(0), ["agent", "docs"], [A, D])), [0]);
});
