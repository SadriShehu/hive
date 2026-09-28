# hive

Track every coding-agent session (Claude Code, opencode, and later others) as one tree:
who spawned whom, what each agent is doing right now, and a key press to jump into
or command any of them. Each agent runs its own real TUI in a tmux window; hive never
wraps or reimplements an agent.

Design doc: https://claude.ai/code/artifact/5df7a9f0-da62-4b84-a527-eec21528bf63

## Status

Phases 1–3 of 5 are done: live tracking, every past session imported and linked to
the session that spawned it, and the tree UI to jump into, message, start, reopen and
stop agents. The agent-facing commands (`hive new --wait`, `send`, `tail`) come next.

## Install

```sh
go install github.com/sadrishehu/hive@latest   # or `go install .` in a checkout
hive install                                   # connect every agent found on PATH
```

`hive install` adds hooks to `~/.claude/settings.json` (backed up to
`settings.json.bak-hive`) and writes `~/.config/opencode/plugin/hive.js`. It is safe
to rerun; `hive uninstall` removes exactly what it added. Sessions that were already
running pick the hooks up after a restart; until then they show as untracked, or are
adopted as soon as they spawn another agent.

## The tree

Run `hive`. Inside tmux it opens in the current pane; outside tmux it attaches to a
tmux session called `hive`, with the tree in its first window. With the popup key
bound (`hive install tmux`), **prefix + a** opens the tree over whatever you're doing
and closes once you jump somewhere.

```
 hive  ● 3 live  ·  203 sessions                                           last 24h
▾ ● claude   Admin phase 2                          2m │ claude Admin phase 2
├─   ● opencode admin-phase2-backend           run   1m │ ● working · interactive · pane %1 · ↵ jumps there
│  └─   ● opencode explore handlers            sub   1m │ ~/go/src/…/kudoesim
└─   ○ claude   lint fix                        run  30m │ ────────────────────────────────
                                                        │ › implement the backend plan
                                                        │   ⚙ Bash: go test ./...
                                                        │ All gates green.
```

The right side is live: the screen of the session's tmux pane, or the end of its
transcript for a headless run, a subagent or a finished session.

| Key | Does |
| --- | --- |
| `↵` | jump to the session's pane (a subagent: its parent's); reopen it if it isn't running |
| `s` | send a message: typed into its pane, or reopens it with the message |
| `n` / `c` | start an agent / start one as a child of the selected session |
| `r` | reopen a finished session in its tool's own TUI |
| `x` | stop it (asks first); a window hive opened closes with it |
| `/` | filter by title, folder or ID, across all history |
| `a` · `i` · `←/→` | all history or last 24h · hide subagents · fold |
| `y` · `S` · `tab` · `?` | copy ID · sync now · hide preview · help |

New agents open in a window of the current tmux session running the tool's real TUI,
linked under the selected session for `c`. Claude is given its session ID up front,
so it is in the tree at once; opencode shows as a new session until its first message.

## Use from the shell

```sh
hive ls           # trees with anything running or active in the last 24h
hive ls --all     # every session ever recorded
hive ls --live    # only running sessions and their ancestors
hive ls --json    # flat list in tree order, for scripts and agents
hive sync         # import history now (hive ls does this on its own); --full re-reads all
```

```
● claude        Admin phase 2                      working    2m  ~/go/src/…/kudoesim     claude:48873400
├─ ● opencode   admin-phase2-backend          run  working    1m  …/kudoesim/backend      opencode:ses_f18cd743
│  └─ ◉ claude  lint-fix                      run  idle       1m  …/kudoesim/backend      claude:0d683b5d
└─ ○ opencode   admin-phase2-app              run  exited    40m  …/kudoesim/admin        opencode:ses_f18cd367
```

● working · ◆ needs you · ◉ idle · ◌ running or untracked · ○ exited.
`run` marks a headless session, `sub` a tool's own subagent.

## How linking works

Any agent can spawn any agent, in any direction; linking lives in hive's core, not in
the adapters. When a session first reports in, its parent is the first of:

1. the parent the event names (opencode's own subagents, Claude's `SubagentStart`);
2. the launch record hive made for the session's tmux pane;
3. the nearest ancestor process that is a live session;
4. `HIVE_PARENT`, which every connected agent exports to the commands it runs;
5. an adapter's own variable, such as `CLAUDE_CODE_SESSION_ID`.

### Sessions from before hive

`hive sync` (run by `hive ls`) imports each tool's own history: Claude transcripts
(`~/.claude/projects`, titles included) and opencode's database, read-only. Only what
changed since the last sync is read, so it takes milliseconds after the first run.

Past spawns are found in the shell commands every session ran, in any direction:
`opencode run --title X` inside Claude, `claude -p` inside opencode, and so on. A
command is matched to the session it started by title first (loop titles like
`review-p$i` match every run), then by start time and folder; a command continuing a
session by ID (`opencode run -s ses_…`) only links sessions nothing else claims.

A running agent that never reported in (started before `hive install`) is matched to
its transcript when that is unambiguous: it is the only such process of its tool in
its folder, and exactly one session there was active since it started.

## Any tool can report

```sh
echo '{"event":"start","session_id":"abc","pid":1234,"title":"refactor","cwd":"/src"}' \
  | hive hook mytool
```

Events: `start`, `prompt`, `busy`, `idle`, `attention`, `end`, `update`. Optional
fields: `parent_id` (`<tool>:<id>`), `internal`, `headless`, `at` (epoch ms). The same
fields work as flags: `hive hook mytool --event start --session abc`.

## Debugging

Hooks never print. Errors go to `~/.local/share/hive/hive.log`; set `HIVE_DEBUG=1` in
an agent's environment to log every linking decision. `HIVE_HOME` moves the database
and log elsewhere; `HIVE_TMUX_SOCKET=name` points hive at a named tmux server
(`tmux -L name`).
