// Pins the web terminal's OSC 8 link click: it opens http(s) links in a new tab
// with no confirm() dialog, and opens nothing for any other scheme.

import { test } from "node:test";
import assert from "node:assert/strict";

import { terminalLinkHandler } from "./terminal_links.js";

function clicks(texts: string[]): [string, string, string][] {
  const opened: [string, string, string][] = [];
  const handler = terminalLinkHandler((url, target, features) => opened.push([url, target, features]));
  const range = { start: { x: 1, y: 1 }, end: { x: 1, y: 1 } };
  for (const t of texts) handler.activate(new Event("click") as MouseEvent, t, range);
  return opened;
}

test("an http or https link opens in a new tab, detached from af", () => {
  assert.deepEqual(clicks(["https://github.com/x/y/pull/1", "http://localhost:8080/"]), [
    ["https://github.com/x/y/pull/1", "_blank", "noopener,noreferrer"],
    ["http://localhost:8080/", "_blank", "noopener,noreferrer"],
  ]);
});

test("a non-http link opens nothing", () => {
  assert.deepEqual(clicks(["javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", "not a url", ""]), []);
});
