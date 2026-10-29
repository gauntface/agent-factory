// Loads every project's backlog for the Backlog view, the Sessions view's item
// chips and the tab's needs-you badge. A backlog.* event names its project by
// repo id, which the web cannot map to every root it knows, so any event
// refetches all projects; there are few. backlog.spend carries the plan use
// itself, so it applies without a refetch.

import { errorText } from "./api.js";
import { getBacklog, getBudget } from "./backlog_api.js";
import type { BacklogView, Budget } from "./backlog_model.js";
import { type Usage, applySpend, spendHasUnmeterable } from "./backlog_usage.js";

export interface BacklogData {
  views: ReadonlyMap<string, BacklogView>;
  errors: ReadonlyMap<string, string>;
  budget: Budget | null;
  usage: Usage | null;
  /** The loader refresh these views came from; a later refresh started later. */
  generation: number;
}

export const EMPTY_BACKLOG_DATA: BacklogData = { views: new Map(), errors: new Map(), budget: null, usage: null, generation: 0 };

const DEBOUNCE_MS = 150;

export interface BacklogLoaderOptions {
  token(): string | null;
  roots(): string[];
  commit(data: BacklogData): void;
  current(): BacklogData;
}

export class BacklogLoader {
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private budgetGeneration = 0;
  private spendGeneration = 0;
  // Whether a backlog.spend since the last GetBudget reply named the
  // unmeterable runs; an older daemon's payload does not.
  private spendUnmeterable = false;

  constructor(private readonly opts: BacklogLoaderOptions) {}

  /** The newest refresh started so far. */
  started(): number {
    return this.generation;
  }

  private latest: Promise<boolean> | null = null;

  /** Refetches every project. A later call wins over one still in flight; the
   *  superseded call resolves only once a newer refresh has committed, so a
   *  caller that waits on it never sees the old state. */
  async refresh(): Promise<void> {
    const token = this.opts.token();
    if (token === null) return;
    const gen = ++this.generation;
    const budgetGen = ++this.budgetGeneration;
    const spendGen = this.spendGeneration;
    let p = this.fetchAndCommit(token, gen, budgetGen, spendGen);
    this.latest = p;
    while (!await p) {
      if (this.latest === p || this.latest === null) return;
      p = this.latest;
    }
  }

  /** Whether this refresh committed. */
  private async fetchAndCommit(token: string, gen: number, budgetGen: number, spendGen: number): Promise<boolean> {
    const roots = [...new Set(this.opts.roots().filter(Boolean))];
    const [results, budget] = await Promise.all([
      Promise.all(roots.map((root) => getBacklog(root, token)
        .then((view) => ({ root, view, error: "" }))
        .catch((e: unknown) => ({ root, view: null, error: errorText(e) })))),
      getBudget(token).catch(() => null),
    ]);
    if (gen !== this.generation || this.opts.token() !== token) return false;
    const cur = this.opts.current();
    const prev = cur.views;
    const views = new Map<string, BacklogView>();
    const errors = new Map<string, string>();
    for (const r of results) {
      if (r.view) views.set(r.root, r.view);
      else {
        errors.set(r.root, r.error);
        const old = prev.get(r.root);
        if (old) views.set(r.root, old);
      }
    }
    const fresh = budgetGen === this.budgetGeneration ? budget : null;
    let usage = cur.usage;
    if (fresh && spendGen === this.spendGeneration) {
      usage = fresh.usage;
      this.spendUnmeterable = false;
    } else if (fresh && cur.usage && !this.spendUnmeterable) {
      usage = { ...cur.usage, unmeterable: fresh.usage.unmeterable };
    }
    this.opts.commit({ views, errors, budget: fresh?.budget ?? cur.budget, usage, generation: gen });
    return true;
  }

  /** Applies a backlog.spend payload. A GetBudget still in flight was read
   *  before it, so that reply's plan use is dropped. */
  applySpend(ev: unknown): void {
    const cur = this.opts.current();
    const usage = applySpend(cur.usage, ev);
    if (usage === cur.usage) return;
    this.spendGeneration++;
    if (spendHasUnmeterable(ev)) this.spendUnmeterable = true;
    this.opts.commit({ ...cur, usage });
  }

  /** Coalesces a burst of events into one refetch. */
  request(): void {
    if (this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.refresh();
    }, DEBOUNCE_MS);
  }

  reset(): void {
    this.latest = null;
    this.generation++;
    this.budgetGeneration++;
    this.spendGeneration++;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }
}
