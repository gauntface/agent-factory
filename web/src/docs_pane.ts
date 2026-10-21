// The Docs tab (proposal mode, step 2): a doc strip, a header with the save
// status and the open-question count, a conflict banner, and the editor. This
// module is the entry of the lazy bundle dist/lazy/docs-editor.js (see build.mjs);
// docs_tab.ts in the main bundle loads it the first time a Docs tab opens. What
// it imports from the main bundle's modules gets a second copy in this bundle, so
// keep those imports small.

import { errorText } from "./api.js";
import { countOpenQuestions } from "./callouts.js";
import { createDocsEditor, type DocsEditor } from "./docs_editor.js";
import {
  changeAction,
  nextQuestionDoc,
  orderDocs,
  questionCountLabel,
  statusText,
} from "./docs_model.js";
import { DocSync, type SyncStatus } from "./docs_sync.js";
import { h } from "./dom.js";
import { icon } from "./icon.js";
import {
  addDoc,
  changeNamesDoc,
  DOC_NAME_PATTERN,
  getDoc,
  getProposal,
  type ProposalChange,
  type ProposalDocRef,
  type ProposalInfo,
  putDoc,
} from "./proposal_api.js";
import { diffText } from "./text_diff.js";

// The doc each proposal last showed, so a rebuilt pane opens where it was.
const lastDoc = new Map<string, string>();

const REFRESH_DEBOUNCE_MS = 300;

export interface DocsPaneOptions {
  sessionId: string;
  slug: string;
  token: string;
  /** Registers for proposal.changed events; returns the unsubscribe. */
  subscribe(fn: (change: ProposalChange) => void): () => void;
}

export class DocsPane {
  private readonly root: HTMLElement;
  private readonly strip: HTMLElement;
  private readonly addButton: HTMLButtonElement;
  private readonly menu: HTMLElement;
  private readonly status: HTMLElement;
  private readonly questions: HTMLButtonElement;
  private readonly banner: HTMLElement;
  private readonly body: HTMLElement;
  private readonly editorHost: HTMLElement;
  private readonly notice: HTMLElement;

  private info: ProposalInfo | null = null;
  private ordered: ProposalDocRef[] = [];
  private current: ProposalDocRef | null = null;
  private sync: DocSync | null = null;
  private editor: DocsEditor | null = null;
  private freshBuffer = true;
  private readonly texts = new Map<string, string>();
  private generation = 0;
  private refreshTimer: number | null = null;
  private disposed = false;
  private readonly unsubscribe: () => void;
  private readonly onDocMouseDown = (e: MouseEvent): void => {
    if (!this.menu.hidden && !this.menu.contains(e.target as Node) && e.target !== this.addButton &&
      !this.addButton.contains(e.target as Node)) {
      this.closeMenu();
    }
  };

  constructor(host: HTMLElement, private readonly opts: DocsPaneOptions) {
    this.strip = h("div", { class: "af-docs-strip", role: "tablist" });
    this.strip.setAttribute("aria-label", "Docs");
    this.addButton = h("button", { type: "button", class: "af-ghost af-docs-add", title: "Add doc" }, icon("plus"));
    this.addButton.setAttribute("aria-label", "Add doc");
    this.addButton.setAttribute("aria-haspopup", "menu");
    this.addButton.setAttribute("aria-expanded", "false");
    this.addButton.addEventListener("click", () => (this.menu.hidden ? this.openMenu() : this.closeMenu()));
    this.menu = h("div", { class: "af-docs-menu", role: "menu", hidden: true });
    this.menu.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        this.closeMenu();
        this.addButton.focus();
      }
    });
    const addWrap = h("div", { class: "af-docs-addwrap" }, this.addButton, this.menu);
    const stripWrap = h("div", { class: "af-docs-stripwrap" }, this.strip, addWrap);
    this.status = h("span", { class: "af-docs-status", role: "status" });
    this.questions = h("button", { type: "button", class: "af-ghost af-docs-questions", hidden: true });
    this.questions.addEventListener("click", () => void this.goToNextQuestion());
    // Step 3 puts the auto-send switch and chip here.
    const slot = h("div", { class: "af-docs-slot" });
    const tools = h("div", { class: "af-docs-tools" }, slot, this.status, this.questions);
    const head = h("div", { class: "af-docs-head" }, stripWrap, tools);
    this.banner = h("div", { class: "af-docs-banner", role: "alert", hidden: true });
    this.editorHost = h("div", { class: "af-docs-editor" });
    this.notice = h("div", { class: "af-docs-notice", hidden: true });
    this.body = h("div", { class: "af-docs-body" }, this.notice, this.editorHost);
    this.root = h("div", { class: "af-docs" }, head, this.banner, this.body);
    this.root.dataset.proposal = opts.slug;
    host.replaceChildren(this.root);
    this.unsubscribe = opts.subscribe((change) => this.handleChange(change));
    document.addEventListener("mousedown", this.onDocMouseDown, true);
    this.setStatus("loading");
    void this.loadProposal();
  }

  dispose(): void {
    this.disposed = true;
    this.unsubscribe();
    document.removeEventListener("mousedown", this.onDocMouseDown, true);
    if (this.refreshTimer !== null) window.clearTimeout(this.refreshTimer);
    this.sync?.dispose();
    this.sync = null;
    this.editor?.destroy();
    this.editor = null;
  }

  private memoryKey(): string {
    return `${this.opts.sessionId}\n${this.opts.slug}`;
  }

  private showNotice(message: string, retry: (() => void) | null): void {
    const children: (Node | string)[] = [h("p", { class: "af-docs-notice-text" }, message)];
    if (retry) {
      const btn = h("button", { type: "button", class: "af-ghost" }, "Retry");
      btn.addEventListener("click", retry);
      children.push(btn);
    }
    this.notice.replaceChildren(...children);
    this.notice.hidden = false;
    this.editorHost.hidden = true;
  }

  private hideNotice(): void {
    this.notice.hidden = true;
    this.editorHost.hidden = false;
  }

  private async loadProposal(): Promise<void> {
    const gen = ++this.generation;
    let info: ProposalInfo;
    try {
      info = await getProposal(this.opts.sessionId, this.opts.slug, this.opts.token);
    } catch (e) {
      if (this.disposed || gen !== this.generation) return;
      this.setStatus("error");
      this.showNotice(`Could not load proposal “${this.opts.slug}”: ${errorText(e)}`, () => void this.loadProposal());
      return;
    }
    if (this.disposed || gen !== this.generation) return;
    this.applyInfo(info);
    const remembered = lastDoc.get(this.memoryKey());
    const first = this.ordered.find((d) => d.name === remembered) ?? this.ordered[0] ?? null;
    if (!first) {
      this.setStatus("saved");
      this.showNotice("This proposal has no docs yet. Add one with +.", null);
      return;
    }
    void this.openDoc(first);
    for (const doc of this.ordered) {
      if (doc.name !== first.name) void this.refreshText(doc);
    }
  }

  private applyInfo(info: ProposalInfo): void {
    this.info = info;
    this.ordered = orderDocs(info.docs);
    if (this.current) {
      this.current = this.ordered.find((d) => d.name === this.current!.name) ?? this.current;
    }
    this.renderStrip();
    this.renderQuestions();
  }

  private renderStrip(): void {
    const tabs = this.ordered.map((doc) => {
      const selected = doc.name === this.current?.name;
      const tab = h("button", { type: "button", class: "af-docs-tab", role: "tab" }, doc.name);
      tab.setAttribute("aria-selected", selected ? "true" : "false");
      tab.tabIndex = selected ? 0 : -1;
      tab.title = doc.file;
      tab.addEventListener("click", () => void this.openDoc(doc));
      tab.addEventListener("keydown", (e) => this.onStripKey(e, doc));
      return tab;
    });
    this.strip.replaceChildren(...tabs);
  }

  private onStripKey(e: KeyboardEvent, doc: ProposalDocRef): void {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const i = this.ordered.findIndex((d) => d.name === doc.name);
    const next = this.ordered[(i + (e.key === "ArrowRight" ? 1 : -1) + this.ordered.length) % this.ordered.length];
    if (next) {
      void this.openDoc(next).then(() => {
        (this.strip.querySelector('[aria-selected="true"]') as HTMLElement | null)?.focus();
      });
    }
  }

  private openMenu(): void {
    const items: HTMLElement[] = [];
    for (const name of this.info?.templates ?? []) {
      const item = h("button", { type: "button", class: "af-docs-menuitem", role: "menuitem" }, name);
      item.addEventListener("click", () => void this.createDoc(name));
      items.push(item);
    }
    const blank = h("button", { type: "button", class: "af-docs-menuitem", role: "menuitem" }, "Blank…");
    blank.addEventListener("click", () => this.showBlankForm());
    items.push(blank);
    this.menu.replaceChildren(...items);
    this.menu.hidden = false;
    this.addButton.setAttribute("aria-expanded", "true");
    items[0]?.focus();
  }

  private closeMenu(): void {
    this.menu.hidden = true;
    this.addButton.setAttribute("aria-expanded", "false");
  }

  private showBlankForm(): void {
    const input = h("input", { type: "text", class: "af-input af-docs-newname", placeholder: "doc-name" });
    input.setAttribute("aria-label", "New doc name");
    input.autocapitalize = "off";
    input.spellcheck = false;
    const error = h("p", { class: "af-docs-menu-error", hidden: true });
    const add = h("button", { type: "submit", class: "af-primary" }, "Add doc");
    const form = h("form", { class: "af-docs-newform" }, input, add, error);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const name = input.value.trim();
      if (!DOC_NAME_PATTERN.test(name)) {
        error.textContent = "Use lowercase letters, digits and dashes, starting with a letter or digit.";
        error.hidden = false;
        input.focus();
        return;
      }
      void this.createDoc(name, error);
    });
    this.menu.replaceChildren(form);
    input.focus();
  }

  private async createDoc(name: string, errorEl?: HTMLElement): Promise<void> {
    try {
      await addDoc(this.opts.sessionId, this.opts.slug, name, this.opts.token);
      const info = await getProposal(this.opts.sessionId, this.opts.slug, this.opts.token);
      if (this.disposed) return;
      this.closeMenu();
      this.applyInfo(info);
      const doc = this.ordered.find((d) => d.name === name);
      if (doc) await this.openDoc(doc);
    } catch (e) {
      if (this.disposed) return;
      const message = `Could not add “${name}”: ${errorText(e)}`;
      if (errorEl) {
        errorEl.textContent = message;
        errorEl.hidden = false;
      } else {
        this.closeMenu();
        this.setStatus("error", message);
      }
    }
  }

  private ensureEditor(): DocsEditor {
    this.editor ??= createDocsEditor(this.editorHost, {
      text: "",
      label: "Doc text",
      onChange: (text) => {
        if (!this.current || this.freshBuffer) return;
        this.texts.set(this.current.name, text);
        this.renderQuestions();
        this.sync?.edit(text);
      },
    });
    return this.editor;
  }

  private async openDoc(doc: ProposalDocRef, toQuestion = false, force = false): Promise<void> {
    if (!force && this.current?.name === doc.name && this.sync) {
      if (toQuestion) this.editor?.goToNextQuestion(true);
      return;
    }
    this.sync?.dispose();
    this.sync = null;
    this.hideBanner();
    this.current = doc;
    lastDoc.set(this.memoryKey(), doc.name);
    this.renderStrip();
    this.setStatus("loading");
    const editor = this.ensureEditor();
    this.hideNotice();
    this.freshBuffer = true;
    const sync = new DocSync({
      load: () => getDoc(this.opts.sessionId, this.opts.slug, doc.name, this.opts.token),
      save: (text, base) => putDoc(this.opts.sessionId, this.opts.slug, doc.name, text, base, this.opts.token),
      setBuffer: (text) => {
        if (this.sync !== sync || !this.editor) return;
        if (this.freshBuffer) {
          this.freshBuffer = false;
          this.editor.setText(text);
        } else {
          this.editor.applyRemote(diffText(this.editor.getText(), text));
        }
        this.texts.set(doc.name, text);
        this.renderQuestions();
      },
      onStatus: (status, detail) => {
        if (this.sync === sync) this.setStatus(status, detail);
      },
      setTimer: (fn, ms) => window.setTimeout(fn, ms),
      clearTimer: (handle) => window.clearTimeout(handle as number),
    });
    this.sync = sync;
    try {
      await sync.start();
    } catch (e) {
      if (this.sync !== sync) return;
      this.showNotice(`Could not load ${doc.file}: ${errorText(e)}`, () => void this.openDoc(doc, toQuestion, true));
      return;
    }
    if (toQuestion && this.sync === sync) editor.goToNextQuestion(true);
  }

  private async refreshText(doc: ProposalDocRef): Promise<void> {
    try {
      const d = await getDoc(this.opts.sessionId, this.opts.slug, doc.name, this.opts.token);
      if (this.disposed || this.current?.name === doc.name) return;
      this.texts.set(doc.name, d.text);
      this.renderQuestions();
    } catch {
      // The count falls back to the daemon's total until a later event succeeds.
    }
  }

  private handleChange(change: ProposalChange): void {
    if (this.disposed || change.slug !== this.opts.slug) return;
    // Any change can add or delete a doc, or move the open-question total.
    this.scheduleListRefresh();
    switch (changeAction(change, this.opts.slug, this.current, this.ordered)) {
      case "open-doc":
        this.sync?.remoteChanged(change.commit);
        break;
      case "other-doc": {
        const doc = this.ordered.find((d) => changeNamesDoc(change, d));
        if (doc) void this.refreshText(doc);
        break;
      }
      case "doc-list":
      case "ignore":
        break;
    }
  }

  private scheduleListRefresh(): void {
    if (this.refreshTimer !== null) window.clearTimeout(this.refreshTimer);
    this.refreshTimer = window.setTimeout(() => {
      this.refreshTimer = null;
      void getProposal(this.opts.sessionId, this.opts.slug, this.opts.token).then((info) => {
        if (this.disposed) return;
        const known = new Set(this.ordered.map((d) => d.name));
        this.applyInfo(info);
        for (const doc of this.ordered) {
          if (!known.has(doc.name)) void this.refreshText(doc);
        }
        const current = this.current;
        if (current && !this.ordered.some((d) => d.name === current.name)) {
          this.sync?.dispose();
          this.sync = null;
          this.current = null;
        }
        if (!this.current && this.ordered[0]) {
          void this.openDoc(this.ordered[0]);
        } else if (!this.current) {
          this.showNotice("This proposal has no docs yet. Add one with +.", null);
        }
      }, () => undefined);
    }, REFRESH_DEBOUNCE_MS);
  }

  private questionCounts(): Map<string, number> {
    const counts = new Map<string, number>();
    for (const doc of this.ordered) {
      const text = this.texts.get(doc.name);
      if (text !== undefined) counts.set(doc.name, countOpenQuestions(text));
    }
    return counts;
  }

  private renderQuestions(): void {
    const counts = this.questionCounts();
    const allKnown = this.ordered.every((d) => counts.has(d.name));
    let total = 0;
    for (const n of counts.values()) total += n;
    if (!allKnown) total = Math.max(total, this.info?.open_questions ?? 0);
    this.questions.hidden = total === 0;
    const label = questionCountLabel(total);
    this.questions.replaceChildren(icon("circle-question"), h("span", {}, String(total)));
    this.questions.title = `${label} · go to the next one`;
    this.questions.setAttribute("aria-label", `${label}. Go to the next one`);
  }

  private async goToNextQuestion(): Promise<void> {
    if (this.editor && this.current && this.editor.goToNextQuestion(false)) return;
    const target = nextQuestionDoc(this.ordered, this.current, this.questionCounts(), false);
    if (!target) return;
    if (target.here) {
      this.editor?.goToNextQuestion(true);
    } else {
      await this.openDoc(target.doc, true);
    }
  }

  private setStatus(status: SyncStatus, detail = ""): void {
    this.status.textContent = statusText(status, detail);
    this.status.dataset.state = status;
    if (status === "conflict") {
      this.showBanner();
    } else if (!this.banner.hidden) {
      this.hideBanner();
    }
  }

  private showBanner(): void {
    const sync = this.sync;
    if (!sync) return;
    const action = (label: string, run: () => Promise<void>): HTMLButtonElement => {
      const btn = h("button", { type: "button", class: "af-ghost af-docs-banner-action" }, label);
      btn.addEventListener("click", () => {
        for (const b of this.banner.querySelectorAll("button")) b.disabled = true;
        run().catch((e: unknown) => {
          for (const b of this.banner.querySelectorAll("button")) b.disabled = false;
          this.status.textContent = `Not saved · ${errorText(e)}`;
        });
      });
      return btn;
    };
    const sep = (): HTMLElement => h("span", { class: "af-docs-sep", ariaHidden: "true" }, " · ");
    this.banner.replaceChildren(
      h("span", { class: "af-docs-banner-text" }, "The agent changed this section too"),
      sep(),
      action("Keep mine", () => sync.keepMine()),
      sep(),
      action("Take theirs", () => sync.takeTheirs()),
      sep(),
      action("Show both", () => sync.showBoth()),
    );
    this.banner.hidden = false;
  }

  private hideBanner(): void {
    this.banner.hidden = true;
    this.banner.replaceChildren();
  }
}
