// Loads every project's backlog for the Backlog view, the Sessions view's item
// chips and the tab's needs-you badge. A backlog.* event names its project by
// repo id, which the web cannot map to every root it knows, so any event
// refetches all projects; there are few.

import { errorText } from "./api.js";
import { getBacklog, getBudget } from "./backlog_api.js";
import type { BacklogView, Budget } from "./backlog_model.js";

export interface BacklogData {
  views: ReadonlyMap<string, BacklogView>;
  errors: ReadonlyMap<string, string>;
  budget: Budget | null;
}

export const EMPTY_BACKLOG_DATA: BacklogData = { views: new Map(), errors: new Map(), budget: null };

export interface BacklogLoaderOptions {
  token(): string | null;
  roots(): string[];
  commit(data: BacklogData): void;
  current(): BacklogData;
}

export class BacklogLoader {
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly opts: BacklogLoaderOptions) {}

  /** Refetches every project. A later call wins over one still in flight. */
  async refresh(): Promise<void> {
    const token = this.opts.token();
    if (token === null) return;
    const gen = ++this.generation;
    const roots = [...new Set(this.opts.roots().filter(Boolean))];
    const [results, budget] = await Promise.all([
      Promise.all(roots.map((root) => getBacklog(root, token)
        .then((view) => ({ root, view, error: "" }))
        .catch((e: unknown) => ({ root, view: null, error: errorText(e) })))),
      getBudget(token).catch(() => this.opts.current().budget),
    ]);
    if (gen !== this.generation || this.opts.token() !== token) return;
    const prev = this.opts.current().views;
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
    this.opts.commit({ views, errors, budget });
  }

  /** Coalesces a burst of events into one refetch. */
  request(): void {
    if (this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.refresh();
    }, 150);
  }

  reset(): void {
    this.generation++;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }
}
