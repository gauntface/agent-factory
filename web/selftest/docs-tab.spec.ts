import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

// Proposal mode, step 2: `af proposal start` gives a session a Docs tab, the web
// renders it with the lazy editor bundle, and an edit in the browser saves through
// the daemon to the doc on disk.
test("docs tab: af proposal start opens a Docs pane whose edits save to disk", async ({ page, request }) => {
  const afBin = process.env.AF_BIN;
  const mockRepo = process.env.AF_MOCK_REPO;
  test.skip(!afBin || !mockRepo, "AF_BIN/AF_MOCK_REPO are set only by web-selftest-entry.sh");
  const af = (...args: string[]): string =>
    execFileSync(afBin as string, args, { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" });

  const title = "docs-probe";
  const slug = "docs-probe-plan";
  af("sessions", "create", "--repo", mockRepo as string, "--name", title, "--program", "claude");
  const started = JSON.parse(af("proposal", "start", slug, "--repo", mockRepo as string, "--session", title,
    "--docs", "goals,api")) as { path: string; tab?: string; tab_error?: string };
  expect(started.tab_error ?? "", "start must create the Docs tab").toBe("");
  expect(started.tab).toBeTruthy();

  const envelope = await (await request.post("/v1/Snapshot", { data: { repo_id: "" } })).json();
  const session = envelope.data.instances.find((s: { title: string }) => s.title === title);
  expect(session).toBeTruthy();

  const editorLoads: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/lazy/docs-editor.js")) editorLoads.push(r.url());
  });
  await page.goto(`/#/session/${session.id}`);
  await expect(page.locator(".af-tab-label", { hasText: "Agent" })).toBeVisible();
  expect(editorLoads, "the editor bundle must not load before a Docs tab opens").toHaveLength(0);

  await page.locator(".af-tab", { hasText: "Docs" }).click();
  const pane = page.locator(`.af-docs[data-proposal="${slug}"]`);
  await expect(pane.locator(".cm-content")).toBeVisible();
  await expect(pane.locator(".af-docs-tab")).toHaveText(["goals", "api"]);
  await expect(pane.locator('.af-docs-tab[aria-selected="true"]')).toHaveText("goals");
  await expect(pane.locator(".af-docs-status")).toHaveText("Saved");
  expect(editorLoads).toHaveLength(1);

  const marker = `Edited from the browser ${Date.now()}`;
  await pane.locator(".cm-content").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.type(`\n\n${marker}\n`);
  await expect(pane.locator(".af-docs-status")).toHaveText("Saved", { timeout: 15_000 });
  await expect.poll(() => readFileSync(join(started.path, "goals.md"), "utf8")).toContain(marker);
});
