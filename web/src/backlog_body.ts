// An item body in the CodeMirror live preview from the lazy Docs bundle. Until
// the bundle loads, and if it fails to, the body shows as plain text.

import { type DocsModule, loadDocsModule } from "./docs_tab.js";
import type { DocsEditor } from "./docs_editor.js";

export const SAVE_DELAY_MS = 800;

/** Where the editor bundle comes from; tests swap in a fake. */
export const bodyEditorLoader: { load(): Promise<Pick<DocsModule, "createDocsEditor">> } = { load: loadDocsModule };

export class BodyEditor {
  readonly el: HTMLElement;
  private editor: DocsEditor | null = null;
  private text = "";
  private timer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;

  constructor(private readonly save: (text: string) => void, label: string) {
    this.el = document.createElement("div");
    this.el.className = "af-backlog-body";
    const fallback = document.createElement("pre");
    fallback.className = "af-backlog-body-text";
    this.el.append(fallback);
    bodyEditorLoader.load().then((mod) => {
      if (this.disposed) return;
      this.el.replaceChildren();
      this.editor = mod.createDocsEditor(this.el, { text: this.text, label, onChange: (t) => this.changed(t) });
    }, () => {
      fallback.textContent = this.text;
    });
  }

  /** Shows the stored body. An edit the user is typing is kept. */
  setText(text: string): void {
    if (this.timer !== null || text === this.text) return;
    this.text = text;
    if (this.editor) {
      if (this.editor.getText() !== text) this.editor.setText(text);
    } else {
      const pre = this.el.querySelector("pre");
      if (pre) pre.textContent = text;
    }
  }

  private changed(text: string): void {
    this.text = text;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = setTimeout(() => this.flush(), SAVE_DELAY_MS);
  }

  flush(): void {
    if (this.timer === null) return;
    clearTimeout(this.timer);
    this.timer = null;
    this.save(this.text);
  }

  dispose(): void {
    this.flush();
    this.disposed = true;
    this.editor?.destroy();
  }
}
