// Session previews for the Backlog view (backlog v2 proposal, technical.md
// "Session preview"): /v1/PreviewSession text, cached for 5 s, one request at a
// time, and a hover popover on the state chip that waits 300 ms before it asks.

import { af, errorText } from "./api.js";
import { h } from "./dom.js";

export const PREVIEW_DELAY_MS = 300;
export const PREVIEW_TTL_MS = 5000;
export const PREVIEW_LINES = 12;

export interface Preview {
  text: string;
  error?: string;
}

export async function previewSession(id: string, lines: number, token: string): Promise<string> {
  const resp = await af<{ text?: string }>("PreviewSession", { id, lines }, token);
  return resp?.text ?? "";
}

/** Previews by session id. A request asked for while another is out waits for
 *  it; a newer ask replaces a waiting one, which then resolves null. */
export class PreviewCache {
  private readonly entries = new Map<string, { preview: Preview; at: number }>();
  private busy = false;
  private queued: { id: string; resolve: (p: Preview | null) => void } | null = null;

  constructor(private readonly load: (id: string) => Promise<string>, private readonly now: () => number = Date.now) {}

  cached(id: string): Preview | undefined {
    const e = this.entries.get(id);
    return e && this.now() - e.at < PREVIEW_TTL_MS ? e.preview : undefined;
  }

  get(id: string): Promise<Preview | null> {
    const hit = this.cached(id);
    if (hit) return Promise.resolve(hit);
    if (this.busy) {
      if (this.queued?.id === id) {
        const q = this.queued;
        return new Promise((resolve) => {
          const prev = q.resolve;
          q.resolve = (p) => { prev(p); resolve(p); };
        });
      }
      this.queued?.resolve(null);
      return new Promise((resolve) => { this.queued = { id, resolve }; });
    }
    return this.run(id);
  }

  private async run(id: string): Promise<Preview> {
    this.busy = true;
    let preview: Preview;
    try {
      preview = { text: await this.load(id) };
    } catch (e) {
      preview = { text: "", error: errorText(e) };
    }
    this.entries.set(id, { preview, at: this.now() });
    this.busy = false;
    const q = this.queued;
    this.queued = null;
    if (q) void this.get(q.id).then(q.resolve);
    return preview;
  }
}

/** One floating preview for the list's state chips. */
export class PreviewPopover {
  readonly el: HTMLElement;
  private readonly heading = h("div", { class: "af-backlog-preview-head" });
  private readonly body = h("pre", { class: "af-backlog-preview-text" });
  private timer: ReturnType<typeof setTimeout> | null = null;
  private anchor: HTMLElement | null = null;
  private sessionId = "";

  constructor(private readonly cache: PreviewCache) {
    this.el = h("div", { class: "af-backlog-preview", role: "tooltip", hidden: true }, this.heading, this.body);
    this.el.id = "af-backlog-preview";
  }

  /** Wires an anchor to show the session's preview on hover or focus. */
  attach(anchor: HTMLElement, sessionId: string, heading: string): void {
    anchor.dataset.previewSession = sessionId;
    const show = (): void => this.schedule(anchor, sessionId, heading);
    const hide = (): void => {
      if (this.anchor === anchor) this.hide();
    };
    anchor.addEventListener("mouseenter", show);
    anchor.addEventListener("focus", show);
    anchor.addEventListener("mouseleave", hide);
    anchor.addEventListener("blur", hide);
  }

  /** After a list rebuild: follows the open preview to its new chip, or hides it. */
  rebind(root: HTMLElement): void {
    if (!this.anchor) return;
    const next = [...root.querySelectorAll<HTMLElement>("[data-preview-session]")]
      .find((el) => el.dataset.previewSession === this.sessionId);
    if (!next) {
      this.hide();
      return;
    }
    this.anchor = next;
    if (!this.el.hidden) next.setAttribute("aria-describedby", this.el.id);
  }

  hide(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
    this.anchor?.removeAttribute("aria-describedby");
    this.anchor = null;
    this.sessionId = "";
    this.el.hidden = true;
  }

  private schedule(anchor: HTMLElement, sessionId: string, heading: string): void {
    if (this.anchor === anchor) return;
    this.hide();
    this.anchor = anchor;
    this.sessionId = sessionId;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.cache.get(sessionId).then((p) => {
        if (!p || this.sessionId !== sessionId || !this.anchor?.isConnected) return;
        this.show(this.anchor, heading, p);
      });
    }, PREVIEW_DELAY_MS);
  }

  private show(anchor: HTMLElement, heading: string, p: Preview): void {
    this.heading.textContent = heading;
    this.body.textContent = p.error ? `Preview unavailable · ${p.error}` : p.text || "No output yet";
    this.body.classList.toggle("af-backlog-preview-error", Boolean(p.error));
    const r = anchor.getBoundingClientRect();
    const vw = typeof window === "undefined" ? 0 : window.innerWidth;
    const vh = typeof window === "undefined" ? 0 : window.innerHeight;
    const below = vh === 0 || r.top + r.height < vh * 0.6;
    this.el.style.top = below ? `${r.top + r.height}px` : "auto";
    this.el.style.bottom = below ? "auto" : `${vh - r.top}px`;
    this.el.style.left = vw === 0 ? `${r.left}px` : "auto";
    this.el.style.right = vw === 0 ? "auto" : `${Math.max(0, vw - (r.left + r.width))}px`;
    this.el.hidden = false;
    anchor.setAttribute("aria-describedby", this.el.id);
  }
}
