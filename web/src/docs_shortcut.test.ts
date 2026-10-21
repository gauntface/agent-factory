import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { docsShortcuts, docsTabIndexes } from "./docs_shortcut.js";
import { TabKind } from "./types.js";

class Element extends EventTarget {
  children: Element[] = [];
  className = "";
  title = "";
  type = "";
  textContent = "";
  dataset: Record<string, string> = {};
  attrs = new Map<string, string>();
  blurred = false;
  constructor(readonly tagName = "div") { super(); }
  append(...children: (Element | string)[]) {
    for (const child of children) {
      if (typeof child === "string") this.textContent += child;
      else this.children.push(child);
    }
  }
  setAttribute(key: string, value: string) { this.attrs.set(key, value); }
  getAttribute(key: string) { return this.attrs.get(key) ?? null; }
  blur() { this.blurred = true; }
  click() { this.dispatchEvent(new Event("click")); }
}
Object.assign(globalThis, {
  document: {
    createElement: (tag: string) => new Element(tag),
    createElementNS: (_ns: string, tag: string) => new Element(tag),
  },
});

const tabs = [
  { kind: TabKind.Agent, name: "agent" },
  { kind: TabKind.Shell, name: "shell" },
  { kind: TabKind.Docs, name: "docs" },
  { kind: TabKind.Web, name: "web" },
  { kind: TabKind.Docs, name: "auth-rework" },
];

test("docsTabIndexes lists only Docs tabs, in bar order", () => {
  assert.deepEqual(docsTabIndexes(tabs), [2, 4]);
  assert.deepEqual(docsTabIndexes(tabs.slice(0, 2)), []);
});

test("each Docs tab gets a named header button that opens it in one click", () => {
  const opened: number[] = [];
  const buttons = docsShortcuts(tabs, (i) => opened.push(i)) as unknown as Element[];
  assert.equal(buttons.length, 2);
  assert.deepEqual(buttons.map((b) => b.getAttribute("aria-label")), ["Open Docs", "Open auth-rework"]);
  assert.ok(buttons.every((b) => b.className.split(" ").includes("af-term-docs")));
  assert.ok(buttons.every((b) => b.children[0]?.getAttribute("data-icon") === "file-text"));
  buttons[1].click();
  buttons[0].click();
  assert.deepEqual(opened, [4, 2]);
  assert.ok(buttons.every((b) => b.blurred), "the opened tab owns input next");
});

const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "styles.css"), "utf8");

/** The declarations of every rule whose selector list contains `selector`. */
function rules(selector: string): string[] {
  const out: string[] = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = m[1].replace(/\/\*[\s\S]*?\*\//g, "").split(",").map((s) => s.trim());
    if (selectors.includes(selector)) out.push(m[2]);
  }
  return out;
}

/** The body of the first `@media (max-width: 768px)` block holding `selector`. */
function phoneRules(selector: string): string {
  for (const m of css.matchAll(/@media \(max-width: 768px\) \{/g)) {
    let depth = 1;
    let i = m.index + m[0].length;
    const start = i;
    while (depth > 0 && i < css.length) {
      if (css[i] === "{") depth++;
      else if (css[i] === "}") depth--;
      i++;
    }
    const body = css.slice(start, i - 1);
    const rule = body.match(new RegExp(`(?:^|\\})\\s*${selector.replace(/[.[\]]/g, "\\$&")}\\s*\\{([^}]*)\\}`));
    if (rule) return rule[1];
  }
  return "";
}

test("Docs shortcuts show only in the phone session header", () => {
  assert.match(rules(".af-term-docs-group").join(""), /display:\s*none/);
  assert.match(phoneRules(".af-session-first .af-term-docs-group"), /display:\s*flex/);
  assert.match(phoneRules(".af-session-first .af-term-docs"), /display:\s*inline-flex/);
});
