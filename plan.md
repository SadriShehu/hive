# Agent teams — plan

A team is a named group of agents that work on one goal in one repo. Each member is
a real Claude Code, Codex, opencode or Copilot CLI session in its own tmux window, as
every hive session is today. hive holds what the team shares: who is on it, the goal,
the task board and the messages between members. Nothing new runs in the background.

This is the plan. The [README](README.md) describes what ships; DESIGN.md gets a
section when the code lands.

## What stays the same

- hive opens each member with the tool's own command, in a tmux window, and gets out
  of the way. A member is a plain session: `hive send`, `hive tail`, `hive wait`, jump,
  stop and trash work on it as before.
- No daemon. Hooks, `hive team …` commands and the tree's refresh drive everything.
- One database. Teams, members, messages and tasks are tables in `hive.db`.
- Any tool. A team mixes tools; a member can be `auto`, so fusion picks its model.

The only things that enter a member's native session are text that hive types into its
pane: the brief at start, and messages from teammates. The only things that leave are
the `hive team …` commands the member runs and its hook events. So the agents stay
native, and the team lives in one place: hive's database and the tree.

## Concepts

| Term | Meaning |
| --- | --- |
| team | a name, a goal, a folder, a workspace mode, members, tasks and messages |
| member | a role name (`lead`, `backend`, `reviewer`) that points at one session. The role is the address: messages go to roles, not session IDs, so a member resumed under a new session keeps its mail |
| lead | the member that plans and integrates: an agent hive starts, the agent that created the team (`--lead self`), or nobody (`--lead none`: you lead from the tree) |
| brief | the first prompt hive composes for a member: the goal, its part, the roster, and how to use `hive team` |
| mailbox | messages between members, stored by hive and typed into the recipient's pane when the recipient is idle |
| board | one view of the team: goal, members with status, tasks, latest messages. `hive team board` for agents, the preview pane for you |
| task | a numbered unit of work with an owner, a status and the tasks it waits for |
| workspace | `shared` (every member in the same folder, the default) or `worktrees` (a git worktree and branch per member; the lead merges) |

## How it works

1. You run `hive team new api-v2 --goal "…" --lead claude --member backend=claude:auto --member frontend=codex --member reviewer=opencode`, or press `T` in the tree.
2. hive records the team and its members, then starts each member through the usual
   launch: the tool's own command, in a new tmux window, with the brief as the first
   prompt. The window's environment carries `HIVE_TEAM` and `HIVE_ROLE`, next to
   `HIVE_PARENT`.
3. A member reports in through its hooks. The launch record for its pane names the team
   and the role, so hive links the session to the member the same way it links a child
   to its parent today. Claude and Copilot are linked at once, since hive gives them
   their session ID.
4. Members read the board and take tasks: `hive team board`, `hive team task claim 4`.
   hive resolves who is calling from the process tree, as `hive new --parent auto` does
   today: a command run by a member's own subagent counts as the member's.
5. A member writes to another with `hive team say frontend "…"`. hive stores the message.
   If the recipient is idle in a pane, hive types it there at once. If the recipient is
   working or waiting on you, the message waits.
6. When the recipient's turn ends, its `idle` hook event reaches `hive hook`, which
   starts `hive team deliver <session>` as a detached process and returns. That process
   types every queued message into the pane as one paste. The agent reads them as its
   next prompt and answers with `hive team say`.
7. `hive team task done 4 --note "…"` closes a task. hive posts a system message to the
   lead and to the owners of tasks that are now ready, so work moves on without polling.
8. The lead runs `hive team done api-v2` when the goal is met. The team stays in the tree,
   folded and dimmed, with its history; `d` trashes it with its members.

## What it looks like

### The tree

A team is a node of its own. Members sit under it as a flat list; a member's own
subagents and spawned agents stay under the member. The team node's preview is the board.

```
 hive  ● 5 live  ·  ◆ 1 need you  ·  1 team  ·  214 sessions                                last 24h
▾ ⬡ api-v2    add the admin API: list, invite, remove users   4 members · 3/7 tasks   1m │ ⬡ api-v2  ·  add the admin API: list, invite, remove users
├─ ● claude   lead · waits for #4 and #5                                            2m │ 4 members · 3 of 7 tasks done · shared folder · ~/go/src/…/kudoesim
├─ ● claude   backend · #4 handlers for /admin/users                                1m │ started 25m ago by you · ~$3.10
├─ ◆ codex    frontend · #5 admin table view                                       30s │ ──────────────────────────────────────────────────────────
└─ ◉ opencode reviewer · waits for work                                             3m │ ● lead      claude    working    waits for #4 and #5
▾ ● claude   Admin phase 2                                                         12m │ ● backend   claude    working    #4 handlers for /admin/users
└─   ○ claude   lint fix                                                    run    30m │ ◆ frontend  codex     needs you  #5 admin table view
                                                                                        │ ◉ reviewer  opencode  idle       waits for work
                                                                                        │ ──────────────────────────────────────────────────────────
                                                                                        │ tasks
                                                                                        │ ✓ #1 schema for admin users                        backend
                                                                                        │ ✓ #2 migration                                     backend
                                                                                        │ ✓ #3 models                                        backend
                                                                                        │ ● #4 handlers for /admin/users                     backend
                                                                                        │ ● #5 admin table view                              frontend
                                                                                        │ ○ #6 integration tests               after #4 #5   reviewer
                                                                                        │ ○ #7 integrate and open the PR       after #6      lead
                                                                                        │ ──────────────────────────────────────────────────────────
                                                                                        │ messages
                                                                                        │  1m  backend → frontend  the list endpoint is GET /admin/users?page=
                                                                                        │  2m  hive → lead         #3 done by backend; #4 and #5 are open
                                                                                        │  9m  lead → all          tasks are up; backend starts with #1
                                                                                        │ 25m  you → lead          start with the schema
```

A member row keeps today's columns. Its title is `role · what it does now`: the task it
claimed, else its session title. The header counts teams.

Keys on a team node:

| Key | Does |
| --- | --- |
| `↵` | jump to the member that needs you, else the lead, else the first live member |
| `s` | message everyone (`you → all`), through the mailbox |
| `x` | stop every member (asks first) |
| `d` | trash the team and its members (asks first; refused while a member runs) |
| `←` / `→` | fold and unfold the members |

Keys on a member row work as on any session. `s` goes through the mailbox (`you →
backend`), so the message is in the team's log and is typed only when the member is idle.
`T` opens the new-team form anywhere.

### The new-team form

```
 New team
 › name     api-v2
   folder   ~/go/src/github.com/kudoesim
   work     ● shared folder   ○ a worktree per member
   lead     claude · default                      ← → cycles; none: you lead from the tree
   members  backend=claude:auto frontend=codex reviewer=opencode
   goal     add the admin API: list, invite and remove users.
            backend owns internal/ and the database; frontend owns web/.

                                                   enter starts the team · esc cancels
```

The first version takes the members as one line, in the CLI's syntax. The second
version gets one row per member: role, tool, model and the member's own prompt, with
`+` to add a row and templates from config to fill them.

### From the shell

```sh
$ hive team new api-v2 --lead claude \
    --member backend=claude:auto --member frontend=codex --member reviewer=opencode \
    --goal "add the admin API: list, invite and remove users"
team api-v2: 4 members started in ~/go/src/github.com/kudoesim
  lead      claude:7e21…   pane %12
  backend   claude:0a9c…   pane %13   picked claude-sonnet-5-5: the lightest of 24 that meet coding 6
  frontend  codex          pane %14   reports in with its first event
  reviewer  opencode       pane %15   reports in with its first event

$ hive team say lead "start with the schema"
queued for lead; it is working

$ hive team board api-v2 --json | jq '.tasks[] | select(.status == "open")'
```

### The brief

The first prompt of each member. hive composes it from the team and the member:

```
You are backend on the hive team api-v2 (lead · claude, backend · claude, frontend · codex,
reviewer · opencode), in /Users/me/go/src/github.com/kudoesim.

Goal: add the admin API: list, invite and remove users.
Your part: you own internal/ and the database.

The team works through hive. `hive team board` shows everything: the goal, who does what,
the tasks and the latest messages.
- Tasks: `hive team task claim <n>` takes an open task. `hive team task done <n> --note "what
  changed"` closes it. `hive team task add "title" --owner <role> --after <n>` adds one.
- Messages: `hive team say <role> "text"` writes to one teammate; `hive team say all "text"`
  to everyone. Messages to you arrive here as lines that start with [api-v2 · from <role>].
  Answer them with `hive team say`, not here.
- When a task is done or you are blocked, say so with `hive team say`, then end your turn.
  hive wakes you here when someone writes to you.
- Everyone works in the same folder. Pull before a task; commit small.
Start with `hive team board`, then claim your first task.
```

The lead's brief adds: "You lead. Turn the goal into tasks with `hive team task add`, give
each an owner and an order with `--after`. Review what is marked done, integrate, and run
`hive team done api-v2` when the goal is met." With `--lead none` every brief says that
the human leads from the hive tree and that tasks come from the board.

The brief is the one place where hive talks to an agent. The tools stay native.

### A delivered message

```
[api-v2 · from backend] the list endpoint is GET /admin/users?page=
[api-v2 · from hive] #3 done by backend. Open now: #4 handlers (backend), #5 table view (frontend).
```

Every line starts with `[`, so a message can never run a slash command (`/exit`), a
shell escape (`!`) or a memory shortcut (`#`) in the recipient's TUI. Several queued
messages go in one paste, one line each, with one Enter at the end.

## CLI

| Command | Does |
| --- | --- |
| `hive team new <name> [--goal text\|-] [--cwd dir] [--lead tool[:model]\|self\|none] [--member role=tool[:model]]... [--worktrees] [--from template] [--wait]` | create the team and start its members; print the roster |
| `hive team ls [--json]` | the teams, with members, status and task counts |
| `hive team board [<team>] [--json]` | goal, members with status, tasks, latest messages. Inside a member, the team is implied |
| `hive team say <role\|all> [text\|-]` | message a teammate or everyone, as the calling member (or `you` from a shell; `--as <role>` for scripts) |
| `hive team inbox [--wait] [--timeout d]` | print the calling member's undelivered messages, and mark them delivered; `--wait` blocks for the next one |
| `hive team wait [--task n] [--from role] [--any] [--timeout d]` | block until a task closes, a message arrives, or anything changes on the board; print what happened |
| `hive team task add <title> [--body text] [--owner role] [--after n,...]` | add a task; hive messages the owner when the task is ready |
| `hive team task claim <n>` · `done <n> [--note text]` · `block <n> --why text` · `ls [--json]` | work the board; `claim` refuses a task whose `--after` tasks are open |
| `hive team add <team> <role>=<tool>[:<model>] [-p text]` | start one more member with a brief |
| `hive team rm <team> <role>` | retire a member: stop it, keep its history, deliver no more mail to it |
| `hive team stop <team>` | stop every member (`--yes` without a terminal) |
| `hive team done <team> [--clean]` | mark the goal met; `--clean` removes merged worktrees |
| `hive team log <team> [-n 40] [--json]` | the team's messages, oldest first |
| `hive team deliver <session>` | for hooks: type the session's queued messages into its pane (hidden) |

`hive ls --json` rows gain `team` and `role`. `hive status` counts members as sessions,
as today.

## Data model

New tables, in one migration. They use `CREATE TABLE IF NOT EXISTS`, like the trash, so
an older hive that sets the version back does no harm.

```sql
CREATE TABLE IF NOT EXISTS teams (
  id          TEXT PRIMARY KEY,                 -- short random id; commands take the name or a prefix
  name        TEXT NOT NULL,                    -- unique among teams not in the trash
  goal        TEXT NOT NULL DEFAULT '',
  cwd         TEXT NOT NULL,
  workspace   TEXT NOT NULL DEFAULT 'shared',   -- shared | worktrees
  created_by  TEXT NOT NULL DEFAULT '',         -- the session that ran `hive team new`, if an agent did
  status      TEXT NOT NULL DEFAULT 'active',   -- active | done
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS team_members (
  team_id     TEXT NOT NULL,
  role        TEXT NOT NULL,                    -- the address: lead, backend, …
  session_id  TEXT NOT NULL DEFAULT '',         -- the current session; moves when the member is resumed under a new ID
  tool        TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  prompt      TEXT NOT NULL DEFAULT '',         -- the member's own part of the brief
  cwd         TEXT NOT NULL DEFAULT '',         -- its worktree, when the team uses them
  joined_at   INTEGER NOT NULL,
  left_at     INTEGER NOT NULL DEFAULT 0,       -- retired with `hive team rm`
  PRIMARY KEY (team_id, role)
);
CREATE INDEX IF NOT EXISTS team_members_session ON team_members(session_id);
CREATE TABLE IF NOT EXISTS team_messages (
  id          INTEGER PRIMARY KEY,
  team_id     TEXT NOT NULL,
  from_role   TEXT NOT NULL,                    -- a role, 'you', or 'hive'
  to_role     TEXT NOT NULL,                    -- a role, or 'all'
  body        TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS team_deliveries (    -- one row per recipient; 'all' expands here
  message_id   INTEGER NOT NULL,
  role         TEXT NOT NULL,
  delivered_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (message_id, role)
);
CREATE TABLE IF NOT EXISTS team_tasks (
  team_id     TEXT NOT NULL,
  n           INTEGER NOT NULL,                 -- #1, #2 … per team
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open',     -- open | claimed | done | blocked
  owner       TEXT NOT NULL DEFAULT '',
  after       TEXT NOT NULL DEFAULT '[]',       -- JSON: the task numbers it waits for
  note        TEXT NOT NULL DEFAULT '',         -- what done changed, or why blocked
  created_by  TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (team_id, n)
);
CREATE TABLE IF NOT EXISTS launch_teams (       -- next to launches: which member a pane was opened for
  pane        TEXT PRIMARY KEY,
  team_id     TEXT NOT NULL,
  role        TEXT NOT NULL
);
```

The trash takes a team with its members: `trash` gets the team's id too, and a team in
the trash hides its node and its members. Purging a team purges its members through
their tools, as today, then its rows.

## Delivery rules

The mailbox is the heart of it, and the one piece that touches a running agent. The
rules, in order:

| Recipient is | hive does |
| --- | --- |
| idle, in a pane | types the message now, from the sender's `hive team say` process; records it in `inputs` so `hive wait` on that session waits for the answer |
| working | queues it. The recipient's `idle` hook hands `hive team deliver` the pane; it waits one second for the TUI to settle, checks the status is still idle, then types every queued message as one paste |
| waiting on you (attention) | queues it. Typing into a permission prompt would answer the prompt |
| of unknown status (a tool without turn hooks), in a pane | types it now and marks the delivery blind; the board shows the mark |
| exited | reopens it with the queued messages as the next prompt, as `hive send` does, unless the member was retired with `hive team rm` |
| the sender | refused: a member cannot message itself; `all` leaves the sender out |

`hive hook` never waits on delivery: it starts `hive team deliver` detached and returns,
so the agent's hook timeout (10 s) is never at risk, and nothing prints into the agent.
`hive team deliver` also runs when a member starts or is resumed, for mail that arrived
while it was down.

## Edge cases

| Case | Handling |
| --- | --- |
| A message body starts with `/`, `!` or `#` | the envelope `[team · from role]` comes first, so the TUI sees plain text |
| A member is resumed under a new session ID (Codex, opencode) | the launch record names the role; the membership moves to the new session, mail follows the role |
| A member's subagent or child runs `hive team say` | hive walks up the parent links to the member; the message is the member's |
| Two members write to each other without end | `hive team mute <role>` pauses delivery to a member; the board shows message counts per member. The brief asks members to prefer the board to chat and to write when done or blocked |
| A task is claimed while its `--after` tasks are open | refused with the open ones named; `--force` overrides |
| A member is started with `auto` | fusion rates the goal plus the member's prompt, and picks the tool or the model as `hive new auto` does; the roster line says what it picked and why |
| An agent creates a team | the team node sits under that session in the tree; `--lead self` makes the creator the lead without starting another agent |
| Two teams in one folder | allowed; the board warns |
| The team's folder is gone | starting a member fails as `hive new` does; the team stays with the error on the board |
| A tool without a `{prompt}` in its start command | the brief is typed into the pane once the session reports in, as `hive send` would |
| Worktrees: a member's branch is not merged at `hive team done --clean` | the worktree and the branch stay; the board lists them |

## Changes by package

| Package | Change |
| --- | --- |
| `internal/store` | `team.go`: the tables above and their queries; the trash and purge learn about teams |
| `internal/team` (new) | pure logic: parsing `role=tool:model`, composing the brief, the envelope, the board model, the delivery decision for a session's status, task readiness |
| `internal/tracker` | `team.go`: start members through `Launch` with team and role; resolve the calling member (`Caller` plus the parent walk); `say`, `deliver`, `task done` with its system messages; `Ingest` writes the membership when a launch record is taken, and calls `OnDeliverable` on `idle` and `start` when mail waits |
| `internal/cmd` | `team.go`: the `hive team` commands; `hook.go` sets `OnDeliverable` to start `hive team deliver` detached; `ls.go` adds `team` and `role` |
| `internal/tree` | `Build` takes the teams: a team node per active team, members as its children, the node under the creator's session or at the root |
| `internal/tui` | `team.go`: the board preview, the new-team form (`T`), keys on team nodes, `s` through the mailbox for members; the header count; rows for team nodes |
| `internal/adapters` | `[[team]]` templates in config.toml, with `[[team.member]]` rows |
| `docs/demo/agent` | scripts that run `hive team …` and answer delivered mail, for live tests and the demo |
| README, DESIGN.md | a "Teams" section each |

## Phases

### Phase 1 — Teams and the mailbox

Scope: the tables; `hive team new`, `ls`, `board`, `say`, `inbox`, `add`, `rm`, `stop`,
`deliver`; the brief; membership through launch records and `HIVE_TEAM`/`HIVE_ROLE`;
delivery by the rules above; the team node with the board preview; the one-line `T`
form; `s` through the mailbox; `hive ls --json` fields.

Tests: store round trips; `team` (parsing, brief, envelope, delivery decisions) as table
tests; tracker with the fake world (a member linked from a launch record; `idle` with
queued mail calls `OnDeliverable`; `say` to an idle pane records an input); the TUI model
through fake ops (team rows, board, keys); a live test with two fake agents on a private
tmux server: say, deliver at idle, resume with mail.

### Phase 2 — The task board

Scope: `hive team task …` with owners and `--after`; system messages on done and ready;
`hive team wait`; the member row shows its task; `hive team log`; `[[team]]` templates;
the full form with one row per member.

Tests: readiness and the messages `done` sends; `wait` against a fake clock; templates
in config round trip; the form in the TUI model.

### Phase 3 — Workspaces and hygiene

Scope: `--worktrees` (a `git worktree` and branch `hive/<team>/<role>` per member, under
`~/.local/share/hive/worktrees/<team>/<role>`); `hive team done [--clean]`; teams in the
trash, restore and purge; `hive team mute`; filter by team; a team in the README demo;
the DESIGN.md section.

Tests: worktree creation and cleanup in a scratch git repo; trash and purge of a team
with the fake tools.

## Decisions to confirm

Each has a default this plan takes. Say if you want the other.

| Question | Default here | The other way |
| --- | --- | --- |
| A message to a working member | wait for its `idle` hook, then type it | type it at once and rely on each tool to queue it. Faster, but Codex, opencode and Copilot queue differently, and a prompt open at that moment takes the text |
| Where members work | the same folder | a worktree per member from day one. Cleaner diffs, but the lead must merge before anything is tested together |
| Who leads | an agent hive starts (`--lead claude`) | you (`--lead none`): fewer tokens, and you dispatch from the tree |
| How the tree shows a team | a team node with the members flat under it | plain parent links: members under the lead. No new node type, but a team without a lead agent has no root, and a member spawned by another member nests oddly |
| Where team messages are typed from | the sender's process, else the recipient's `idle` hook | a `hive team deliver` loop you keep running. Simpler rules, but it is a daemon |
