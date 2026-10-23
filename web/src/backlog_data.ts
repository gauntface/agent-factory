// Loads every project's backlog for the Backlog view, the Sessions view's item
// chips and the tab's needs-you badge. A backlog.* event names its project by
// repo id, which the web cannot map to every root it knows, so any event
// refetches all projects; there are few. backlog.spend changes only the budget.

import { errorText } from "./api.js";
import { getBacklog, getBudget } from "./backlog_api.js";
import type { BacklogView, Budget } from "./backlog_model.js";

export interface BacklogData {
  views: ReadonlyMap<string, BacklogView>;
  errors: ReadonlyMap<string, string>;
  budget: Budget | null;
  /** The loader refresh these views came from; a later refresh started later. */
  generation: number;
}

export const EMPTY_BACKLOG_DATA: BacklogData = { views: new Map(), errors: new Map(), budget: null, generation: 0 };

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
  private budgetTimer: ReturnType<typeof setTimeout> | null = null;
  private budgetGeneration = 0;

  constructor(private readonly opts: BacklogLoaderOptions) {}

  /** The newest refresh started so far. */
  started(): number {
    return this.generation;
  }

  /** Refetches every project. A later call wins over one still in flight. */
  async refresh(): Promise<void> {
    const token = this.opts.token();
    if (token === null) return;
    const gen = ++this.generation;
    const budgetGen = ++this.budgetGeneration;
    const roots = [...new Set(this.opts.roots().filter(Boolean))];
    const [results, budget] = await Promise.all([
      Promise.all(roots.map((root) => getBacklog(root, token)
        .then((view) => ({ root, view, error: "" }))
        .catch((e: unknown) => ({ root, view: null, error: errorText(e) })))),
      getBudget(token).catch(() => null),
    ]);
    if (gen !== this.generation || this.opts.token() !== token) return;
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
    this.opts.commit({ views, errors, budget: budgetGen === this.budgetGeneration ? budget ?? cur.budget : cur.budget, generation: gen });
  }

  /** Refetches only the budget, for backlog.spend. */
  async refreshBudget(): Promise<void> {
    const token = this.opts.token();
    if (token === null) return;
    const gen = ++this.budgetGeneration;
    const budget = await getBudget(token).catch(() => null);
    if (gen !== this.budgetGeneration || this.opts.token() !== token || !budget) return;
    this.opts.commit({ ...this.opts.current(), budget });
  }

  /** Coalesces a burst of events into one refetch. */
  request(): void {
    if (this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.refresh();
    }, DEBOUNCE_MS);
  }

  requestBudget(): void {
    if (this.budgetTimer !== null) return;
    this.budgetTimer = setTimeout(() => {
      this.budgetTimer = null;
      void this.refreshBudget();
    }, DEBOUNCE_MS);
  }

  reset(): void {
    this.generation++;
    this.budgetGeneration++;
    if (this.timer !== null) clearTimeout(this.timer);
    if (this.budgetTimer !== null) clearTimeout(this.budgetTimer);
    this.timer = null;
    this.budgetTimer = null;
  }
}
