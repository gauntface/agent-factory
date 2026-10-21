# Proposal mode: UI design

The web UI only gets this tab, in step 2 and step 3.

## Layout

```
┌───────────────────────────┬───────────────────────────────────────────┐
│ Agent                     │ Docs · auth-rework                        │
│                           │ goals  technical  api  +       ● Auto 2?  │
│  (live terminal)          ├───────────────────────────────────────────┤
│                           │ # API design                              │
│                           │                                           │
│                           │ > [!question] Should tokens expire?       │
│                           │                                           │
│                           │ ## Endpoints                              │
│                           │ …                                         │
│                           │                     Sending in 4s · Hold  │
└───────────────────────────┴───────────────────────────────────────────┘
```

- The Docs tab is a normal tab. The web opens it split to the right of the
  Agent pane, 50/50, with the existing pane-split code. This happens when the
  tab appears while you view the session (`start` or `attach` creates it), and
  when you open a session that already has one.
  - It happens once for each Docs tab in each page load. If you close or move
    the split, it stays that way.
  - A second Docs tab replaces the Docs pane. It does not add a third pane.
  - If no pane shows the Agent tab, a single pane splits to the right and keeps
    its tab. A layout of more than one pane that has no Agent pane does not
    change.
  - At phone width (768px or less), the Docs tab stays a tab.
  - The layout is not persisted, and the daemon is not involved.
- **Doc strip:** one sub-tab for each file, in template order and then
  alphabetical order. The **+** menu lists the templates that are not used
  yet, and **Blank…**.
- **Header right:** the Auto / Manual switch, and a count of open
  `[!question]` callouts. Click the count to go to the next question.

## Editor

- CodeMirror 6 with live-preview decorations. Headings, emphasis, links,
  lists, tables and callouts show rendered. The line that has the cursor
  shows its markdown source.
- Do not use a WYSIWYG editor that converts markdown to a document tree and
  back (TipTap, Milkdown). It changes formatting, and the agent then sees
  edits that nobody made.
- Callout colours come from design tokens: question, todo and decision each
  get a colour.
- Load CodeMirror only when a Docs tab first opens, so the main web bundle
  stays small.

## Saving and live reload

- Autosave runs 500 ms after the last keystroke. A save status shows in the
  header: "Saved", "Saving…", or "Merged agent edits".
- When the agent changes a doc that you have no unsaved edits in, the tab
  reloads it. The cursor and scroll position stay where they were.
- When a merge has a conflict, a banner shows "The agent changed this section
  too · Keep mine · Take theirs · Show both".

## Push chip (step 3)

The header slot holds the Auto / Manual switch and the chip. They render the
`push` status from `GetProposal` and then each `proposal.push` event for the
tab's session (`api.md`, Push). With `"push": null` (the session is not
attached to this proposal), the slot is empty.

| `state` | Auto mode | Manual mode |
|---|---|---|
| `countdown` | "Sending to agent in 4s · Hold". At 0 s, "Sending to agent…" until the next status. | — |
| `waiting_agent` | "Waits for agent". It sends when the agent becomes idle and 10 s have passed since your last edit. | "Waits for agent · Send now" |
| `held` | "Held · Send now". The next edit starts the countdown again. | — |
| `sent` | "Sent to agent" for 4 s, then nothing. | "Sent to agent · Send now" for 4 s |
| `idle` | Nothing. | "Send now" |

- The tab counts down from `due_in_ms`, starting when it receives the status,
  not to `due_at`: the browser clock can be different from the daemon clock.
  The number changes once a second. There is no animation, so reduced motion
  needs no other rule.
- **Hold** calls `HoldProposalPush`. **Send now** calls
  `SendProposalChanges`, and it is active only when `unsent` is more than 0.
  The Auto / Manual switch calls `SetProposalPushMode`. The tab applies each
  reply as the new status.
- While a call runs, the switch and the chip button are disabled. A failed
  call shows "Could not send · <reason>" (or hold, or switch to auto/manual)
  in the chip until the next status.
- A `GetProposal` reply is applied only when no push status arrived after the
  request went out, so a slow reply cannot replace a newer event.
- At phone width, the header tools wrap to their own row.
- The code is `web/src/docs_push.ts` (the pure state machine and its timer,
  tested in `docs_push.test.ts`) and the header slot in `docs_pane.ts`.

## Copy

Follow the repo copy rules: sentence case, `…`, and ` · ` as the separator.
