// Opens a proposal's Docs tab split to the right of the Agent pane (proposal-mode
// ui.md). Pure, so the rule is testable without the DOM SplitView needs.

import { type LayoutNode, leaves, replaceTab, splitLeaf } from "./layout.js";
import { TabKind } from "./types.js";

const AGENT_TAB = 0;

/**
 * Places each Docs tab not yet in `seen` into `tree`, once: an existing Docs pane is
 * reused (a second proposal replaces the first), otherwise the Agent pane splits
 * right, 50/50. A Docs tab already on screen is only recorded.
 *
 * `seen` holds `key(identity)` for every Docs tab handled so far and is mutated, so a
 * split the user later closes or rearranges is never put back. `narrow` (phone width)
 * records the tab without placing it, which keeps the tab-bar behaviour there.
 *
 * With no Agent pane, a single pane splits right (the user keeps the tab they were
 * on); a multi-pane layout the user built is left alone.
 */
export function placeNewDocsTabs(
  tree: LayoutNode,
  tabIds: string[],
  tabKinds: number[],
  seen: Set<string>,
  key: (identity: string) => string,
  narrow: boolean,
): LayoutNode {
  let out = tree;
  tabKinds.forEach((kind, tab) => {
    const k = key(tabIds[tab] ?? "");
    if (kind !== TabKind.Docs || seen.has(k)) {
      return;
    }
    seen.add(k);
    if (narrow) {
      return;
    }
    out = placeDocs(out, tab, tabKinds);
  });
  return out;
}

function placeDocs(tree: LayoutNode, docs: number, tabKinds: number[]): LayoutNode {
  const panes = leaves(tree);
  if (panes.some((l) => l.tab === docs)) {
    return tree;
  }
  const docsPane = panes.find((l) => tabKinds[l.tab] === TabKind.Docs);
  if (docsPane) {
    return replaceTab(tree, docsPane.id, docs);
  }
  const target = panes.find((l) => l.tab === AGENT_TAB) ?? (tree.kind === "leaf" ? tree : null);
  return target ? splitLeaf(tree, target.id, "right", docs) : tree;
}
