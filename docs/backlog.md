# Backlog

For anyone who wants agents to work through a list of items, in order, without
starting each session by hand. After this page you will be able to turn a
proposal into backlog items, queue them, let the runner start them within a
share of your Claude plan, answer the agents' questions, and review their work.

The backlog is one project's work items in one strict order. You decide what is
next by moving items above or below each other: there is no priority number,
so no two items tie. The daemon's **runner** starts a session for the top
queued items, up to a limit you set, while the backlog's use of your Claude
plan stays under its **budget**. You can also start any item by hand at any
time.

Backlog data stays on your machine: one file per project,
`$AF_HOME/repos/<repo id>/backlog.json`, and one global `$AF_HOME/budget.json`.

## Names

af uses one word per concept, the same in the CLI, the web, and agent text.

| Name | Meaning |
|------|---------|
| Backlog | The ordered list of items for one project. |
| Item | One piece of work, shown as `B-12`. Commands take `12` or `B-12`. |
| Parent · child | A parent groups other items. A parent never runs itself; its children do. |
| Waits on | B waits on A: B cannot start until A is done. |
| Queue | Make an item eligible for the runner. |
| Question | A message from an agent that needs you. |
| Review | You check finished work and mark it done. |
| Runner | The part of the daemon that starts sessions for queued items. |
| Run | One stretch of agent work on an item, from a prompt to the next idle. |
| Budget | The share of your Claude plan (5-hour and weekly windows) the backlog may use. |

An af **task** is something else: a cron or watch automation (see
[Tasks](tasks.md)). A task can drive the runner, as shown in
[Run the runner from a cron task](#run-the-runner-from-a-cron-task).

### States

Each item shows one state, from its status and its session:

| State | Meaning |
|-------|---------|
| Draft | New. Every item starts here, including items an agent makes. |
| Queued | You queued it; the runner may start it. |
| Waiting | Queued, but it waits on an item that is not done, or an earlier child of a "run children in order" parent is not done. |
| Starting · Working | A session is starting, or the agent is working. |
| Needs you | The agent asked a question. |
| In review | The agent said it finished, or it stopped without saying so. |
| Stopped | Its session was archived, killed, or parked at a usage limit. The reason shows next to it. |
| Done · Dropped | You marked it done, or dropped it. |

A parent shows the state of its children: Needs you if any child needs you,
Working if any child works, Done when all children are done or dropped.

## The flow

### 1. From a proposal to items

The fastest way to fill a backlog is from a proposal (see
[Web client](web.md) for the Proposal tab). In the Proposal tab, right-click a
doc tab (long-press on a phone) and pick **Make backlog items**. af asks the
attached agent to write:

- one parent item for the proposal, with "run children in order" on, and
- one child per PR in the PR stack of `steps.md`, in stack order.

Every item is a draft. When the proposal changes, pick **Update backlog items**
in the same menu: the agent writes the same batch again, and items with the
same proposal and key are updated, not added again. **Open in Backlog** shows
the items.

You can also ask the agent in its terminal ("make backlog items from this
proposal"), or write the batch yourself:

```bash
af backlog add --file - <<'EOF'
{
  "proposal": "auth-rework",
  "items": [
    {"key": "auth-rework", "title": "Auth rework", "sequential": true},
    {"key": "pr-1", "parent": "auth-rework", "title": "Token store"},
    {"key": "pr-2", "parent": "auth-rework", "title": "Login flow"}
  ]
}
EOF
```

Every item needs a `title`; `body`, `program` and `sequential` are optional.
`key` is local to the file. `parent` and `waits_on` take a key (a string) or an
existing item id (a number, such as `12`); `"12"` or `"B-12"` as a string is
read as a key. The batch is all or nothing, and prints each key's id.

To add one item: `af backlog add "Fix the login redirect" --body -` (the body
comes from stdin), or type a title in the **New item** row of the Backlog view.

### 2. Order and queue

Open **Backlog** in the web header. Drag items with the `⠿` handle, or use
`Alt+↑`/`Alt+↓` to move and `Alt+→`/`Alt+←` to nest and un-nest. From the CLI:

```bash
af backlog list                       # the tree, with states
af backlog move 14 --top              # or --before 9, --after 9, --bottom, --into 3
af backlog edit 14 --waits-on 12,13   # 14 starts only after 12 and 13 are done
af backlog queue 3                    # queue a parent: queues its draft children
```

Only you queue items. A queued item that waits on something shows Waiting and
the item it waits for.

### 3. The runner starts items

The runner is off by default, per project. Turn it on in the runner bar of the
Backlog view, or:

```bash
af backlog runner on --max-running 1
```

On each pass the runner walks the backlog top to bottom and starts a session
for each queued item that waits on nothing, until the project has
`--max-running` items working (1 to 8). Items that do not wait on each other
run in parallel.

Each run is a new session on its own branch from the project's default branch,
titled like `B-14 Token store`. Its first prompt is the item body and the rules
for the agent. Item sessions are local sessions only. In the Sessions view they
carry a `B-14` chip that opens the item.

To start an item yourself, use **Start now** in the item panel or
`af backlog start 14`. It ignores the runner switch and the budget. **Run next**
in the runner bar (or `af backlog run`) starts the top queued item the same way,
but only in a free slot: it never starts more than the project's max running.

### 4. Questions

An agent that needs you runs `af backlog ask --self "…"` and stops. The item and
its parent show Needs you, the Backlog tab shows a count, and the session's
attention dot lights, from any view. Answer in the item panel with **Send
answer**, or:

```bash
af backlog answer 14 "Use the existing session cookie"
```

The answer goes to the oldest open question; `--question <n>` picks another one
(`af backlog show 14` lists them).

af adds the answer to the item's thread and sends it to the session. If the
session is archived, af restores it first. If the session is gone, the answer
stays in the thread; start the item again with `af backlog start 14`. If you
answer in the terminal instead, af closes the question as "Answered in session".

### 5. Review

When the agent finishes, it runs `af backlog done --self --pr <url>` and the
item goes to In review with the PR link. If the agent stops without saying it
is done, the item goes to In review too, with the note "The agent stopped
without saying it was done. Check the session."

Check the work, merge it, then mark the item done in the panel or with
`af backlog done 14`. Done is what lets items that wait on it start. To send
the work back, queue the item again or use **Back to draft**.

### 6. Done

The session stays live after done, so you can follow up in it. With
`af backlog runner --on-done archive`, af archives it once it is idle. The
item's **Runs** list has **Open**, **Archive** and **Restore** for every session
it had.

Other item actions: `af backlog drop 14` (a parent drops its open children),
`af backlog reopen 14`, `af backlog delete 14` (only drafts with no runs and no
thread), and `af backlog note 14 "…"` to add a note to the thread.

## The budget

The budget is the part of your Claude plan (Pro or Max) the backlog may use.
It is global, for all your projects:

| Setting | Default | Meaning |
|---------|---------|---------|
| Share | 50% | The backlog's share of each plan window, 5-hour and weekly. The rest stays yours. |
| Stop at | 90% | Plan use, from any source, at which the runner starts nothing new, so backlog work never walks you into the limit. |
| Cap per run | 10% | How much of the 5-hour window one run may use. A run over the cap is interrupted and its item asks you whether to continue. |
| Dollar limit | off | For accounts with no plan readings (API keys): a limit at API prices instead, per 5-hour and per 7-day window. |

Change them in the runner bar's `⋯` menu, or:

```bash
af backlog budget                          # each window: plan %, backlog %, reset time, reading age
af backlog budget --share 30 --stop-at 85 --max-per-run 5
af backlog budget --usd-limit 10           # 0 turns it off
```

The runner starts nothing new while, in either window, the backlog used its
share, the plan is at the stop-at level, a usage limit is seen, or there is no
plan reading yet. The runner bar says which, and when it resumes. Window resets
start it again on their own. Running sessions are never stopped by the budget,
and you can always start an item by hand; its use still counts. So the backlog
can pass its share by at most `max running × cap per run` per project, plus what
you start by hand.

### Where the readings come from

af reads plan use only from the data Claude Code gives its status line
(`rate_limits`, Claude Code 2.1.80 or later, Pro and Max plans, after the first
reply in a session). It never calls Anthropic's undocumented usage endpoint.

For this, af starts every Claude session it launches with a status line of its
own, `af internal plan-tap`. The tap saves the reading under `$AF_HOME/plan/`,
then runs your own status line command with the same input and prints its
output, so your status line still looks and refreshes as before. af does not
touch your Claude settings files. It cannot add the tap when your `claude`
command already passes `--settings`, or when managed settings set the status
line; then af logs a warning and gets no readings from those sessions.

Plan use is for your whole account. To find the backlog's part, af meters the
API-price cost of every Claude Code transcript on this machine and of the item
runs alone, and takes the backlog's share of the plan percent in that ratio.
Use af cannot see, such as claude.ai chat or other computers, makes the
backlog's part look larger, never smaller.

A new model with no known price makes its runs unmeterable, and the runner
waits. Add the price to `config.toml`, in US dollars per million tokens:

```toml
[backlog.prices."claude-x-9"]
input = 3
output = 15
```

Per-model weekly limits (such as Opus-only limits) are not in the status line
data, so the budget cannot see them; the usage-limit gate still stops the
runner when one is hit.

## Run the runner from a cron task

`af backlog run` does one runner pass now: it starts up to `--count` queued
items (default 1), top first, only in free slots up to max running, and ignores the runner switch and
the budget. With the runner off, an af task can start backlog work on your
schedule instead. A task delivers a prompt to an agent, so keep that agent in
one session with `--target-session`:

```bash
af tasks add --name "Backlog at night" --cron "0 1 * * *" --program claude \
  --target-session backlog-cron \
  --prompt 'Run "af backlog run --count 2" and stop. Do not do the items yourself.'
```

## Agents and their limits

Agents learn the backlog from af's own skill. Any agent can list, show, and add
items; items an agent adds are always drafts. From an item session, af refuses
`queue`, `draft`, `review`, `reopen`, `start`, `run`, `done <id>`, `drop`,
`delete`, `answer`, and changes to the runner or budget, and tells the agent to
ask you instead.

This guard stops a confused agent, not a hostile one. The agent runs as you and
could edit the backlog files directly. Sessions an item session creates are not
metered in v1, so the item prompt tells agents not to create them.

## See also

- [CLI reference](reference/cli.md#af-backlog) for every flag.
- [Tasks](tasks.md) for cron and watch automations.
- [Accounts and usage limits](usage-limits.md) for what happens at a usage
  limit.
