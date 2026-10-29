// "Busy while running" for the Backlog view's controls. A scope names what a
// request changes (one item, one project's settings, the shared budget); while
// a request holds a scope, a second request for it is refused and every control
// marked data-busy shows as disabled, with the one that started it also busy.
// aria-disabled, not disabled: a disabled control drops keyboard focus, and a
// rebuild restores focus only from the active element.

export const itemScope = (root: string, id: number): string => `item\n${root}\n${id}`;
export const projectScope = (root: string): string => `project\n${root}`;
export const BUDGET_SCOPE = "budget";

export class Busy {
  private readonly active = new Map<string, string>();

  constructor(private readonly changed: () => void) {}

  /** What started the request that holds the scope, or undefined when none does. */
  by(scope: string): string | undefined {
    return this.active.get(scope);
  }

  /** Takes the scope for `source`; false when a request already holds it. */
  begin(scope: string, source: string): boolean {
    if (this.active.has(scope)) return false;
    this.active.set(scope, source);
    this.changed();
    return true;
  }

  end(scope: string): void {
    if (this.active.delete(scope)) this.changed();
  }

  /** Runs `fn` holding the scope; undefined without calling it when the scope is held. */
  async run<T>(scope: string, source: string, fn: () => Promise<T>): Promise<T | undefined> {
    if (!this.begin(scope, source)) return undefined;
    try {
      return await fn();
    } finally {
      this.end(scope);
    }
  }
}

export function locked(el: Element): boolean {
  return el.getAttribute("aria-disabled") === "true";
}

/** Marks every data-busy control under `root` for a held scope started by
 *  `source`, or clears the marks when `source` is undefined. */
export function paintBusy(root: ParentNode, source: string | undefined, attr = "busy"): void {
  for (const el of root.querySelectorAll<HTMLElement>(`[data-${attr}]`)) {
    if (source === undefined) {
      el.removeAttribute("aria-disabled");
      el.removeAttribute("aria-busy");
      continue;
    }
    el.setAttribute("aria-disabled", "true");
    if (el.getAttribute(`data-${attr}`) === source) el.setAttribute("aria-busy", "true");
    else el.removeAttribute("aria-busy");
  }
}
