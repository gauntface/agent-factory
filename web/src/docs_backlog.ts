// The Proposal tab's link to the backlog (backlog proposal, ui.md "Proposal
// tab"): whether the proposal already has items. The main bundle owns the
// backlog data; the lazy Docs bundle gets it through the DocsBacklog hooks and
// imports only types and the label from here, so it carries no backlog model.

import type { BacklogView } from "./backlog_model.js";

export interface ProposalItems {
  count: number;
  /** The item to open: the proposal's top item, or 0 when there is none. */
  first: number;
}

export interface DocsBacklog {
  /** null when the session's project is not known; the menu then hides its backlog items. */
  items(): ProposalItems | null;
  open(id: number): void;
}

/** The items whose source is proposal `slug`, and the first of them whose
 *  parent is not one of them (the proposal's parent item). */
export function proposalItems(view: BacklogView | undefined, slug: string): ProposalItems {
  const items = (view?.backlog.items ?? []).filter((it) => it.source?.proposal === slug);
  const ids = new Set(items.map((it) => it.id));
  const top = items.find((it) => !it.parent || !ids.has(it.parent));
  return { count: items.length, first: top?.id ?? 0 };
}

export function backlogItemsLabel(items: ProposalItems): string {
  return items.count > 0 ? "Update backlog items" : "Make backlog items";
}
