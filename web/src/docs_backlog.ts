// The Proposal tab's link to the backlog (backlog proposal, ui.md "Proposal
// tab"): whether the proposal already has items, and the request the doc menu
// sends to the attached agent. It lives in the main bundle; the lazy Docs bundle
// sees only the DocsBacklog hooks, so it does not carry a copy of the backlog
// model.

import { af } from "./api.js";
import type { BacklogView } from "./backlog_model.js";

export interface ProposalItems {
  count: number;
  /** The item to open: the proposal's top item, or 0 when there is none. */
  first: number;
}

export interface DocsBacklog {
  /** null when the session's project is not known; the menu then hides its backlog items. */
  items(): ProposalItems | null;
  /** Sends `prompt` to the Docs tab's session. */
  send(prompt: string): Promise<void>;
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

/** The request the menu sends. It must not start with "Proposal <slug>:", which
 *  the proposal rules reserve for pushed edits. */
export function backlogItemsPrompt(slug: string, items: ProposalItems): string {
  if (items.count > 0) {
    return `Update the backlog items of proposal ${slug} to match the proposal as it is now. Follow the proposal rule for backlog items ("af proposal attach" prints the rules): run the same "af backlog add --file -" batch again with the same keys, so the items are updated, not added again.`;
  }
  return `Make backlog items from proposal ${slug}. Follow the proposal rule for backlog items ("af proposal attach" prints the rules): one draft parent item for the proposal and one draft child per PR in the PR stack of steps.md, written with "af backlog add --file -".`;
}

export async function sendSessionPrompt(sessionId: string, prompt: string, token: string): Promise<void> {
  await af<unknown>("SendPrompt", { id: sessionId, prompt }, token);
}
