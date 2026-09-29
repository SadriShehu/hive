# hive

Track every coding-agent session (Claude Code, opencode, GitHub Copilot CLI, and Codex) as one tree:
who spawned whom, what each agent is doing right now, and a key press to jump into
or command any of them. Each agent runs its own real TUI in a tmux window; hive never
wraps or reimplements an agent.

How it works, and why: [DESIGN.md](DESIGN.md).

## Status

All five phases are done: live tracking of Claude Code, opencode, Copilot CLI and
Codex; every past session imported and linked to the session that spawned it; the
tree UI to jump into, message, start, reopen and stop agents; the same actions as
commands, so agents can run agents of their own; and tools added or changed in a
config file.

## Install

```sh
go install github.com/sadrishehu/hive@latest   # or `go install .` in a checkout
hive install                                   # connect every agent found on PATH
```

`hive install` adds hooks to `~/.claude/settings.json` (backed up to
`settings.json.bak-hive`), writes `~/.config/opencode/plugin/hive.js`, adds
`~/.copilot/hooks/hive.json` for Copilot CLI (or `$COPILOT_HOME/hooks/hive.json`,
backed up to `hive.json.bak-hive` when an existing file is changed), and adds hooks
to `~/.codex/hooks.json` for Codex (or `$CODEX_HOME/hooks.json`, backed up to
`hooks.json.bak-hive`). Codex asks you to trust the new hooks when it next starts.
It is safe to rerun; `hive uninstall` removes exactly what it added. Hooks load when
an agent starts, so running sessions pick them up after a restart; until then they
show as untracked, or are adopted as soon as they spawn another agent.

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
| `d` | delete it and everything under it from hive (asks first); the tool's own files stay |
| `/` | filter by title, folder or ID, across all history |
| `a` · `i` · `←/→` | all history or last 24h · hide subagents · fold |
| `y` · `S` · `tab` · `?` | copy ID · sync now · hide preview · help |

New agents open in a window of the current tmux session running the tool's real TUI,
linked under the selected session for `c`. Claude and Copilot CLI are given their
session IDs up front, so they are in the tree at once; opencode shows as a new session
until its first message, and Codex reports in as soon as its TUI starts a thread.

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
`run` marks a headless session, `sub` a tool's own subagent. Sessions a tool starts
ahead of use (Claude's daemon keeps spares ready for `claude --bg`) stay hidden until
their first message, and the daemon's own processes are never taken for sessions.

Every action in the tree is also a command, so an agent can start interactive
children, talk to them and check on them:

```sh
hive new opencode -p "port the tests" --wait   # start it in its own tmux window; prints its ID
hive send <id> "now run them"                  # type into it; an ended one reopens with the message
hive tail <id> -n 20                           # the end of its transcript (--json for agents)
hive jump <id>                                 # go to its pane, reopening it if it has ended
hive resume <id> -p "carry on"                 # reopen an ended session in the background
hive kill <id>                                 # stop it; a window hive opened closes with it
hive rm <id>                                   # delete it and everything under it from hive's records
hive doctor                                    # check tmux, the database and every agent's hooks
```

An `<id>` is the full ID (`claude:48873400-…`), the tool's own ID, or any unique
prefix of either. `hive new` starts in the current folder (`--cwd` to change it), in
the background (`--focus` to switch to it), linked under the agent running the command
(`--parent none` or `--parent <id>` to change that). `--wait` returns once the agent has
reported in, so a `hive send` right after it isn't typed before the agent can read it.
A tool that starts its session only with its first message (opencode without `-p`)
gets a stand-in ID, `opencode:pid-N`, which the other commands accept and which
names the session once it starts.

Deleting a session (`d` in the tree, `hive rm`) removes it and every session under it
from hive's records only; the tool's own transcript stays where the tool keeps it.
Everything in there must have ended first. A deleted session stays out of history
imports, and comes back only if its agent reports in again.

## How linking works

Any agent can spawn any agent, in any direction; linking lives in hive's core, not in
the adapters. When a session first reports in, its parent is the first of:

1. the parent the event names (opencode's own subagents, Claude's `SubagentStart`);
2. the launch record hive made for the session's tmux pane;
3. the nearest ancestor process that is a live session;
4. `HIVE_PARENT`, exported by integrations that can pass it to their shell commands;
5. an adapter's own variable, such as `CLAUDE_CODE_SESSION_ID`.

Copilot CLI does not expose a session-ID environment file or child-shell export
hook, so its children use process ancestry when the parent is still running.

Codex runs its interactive sessions through a shared `codex app-server` daemon, and
its hooks run there, not under the TUI in your pane. hive never treats the daemon as
a session: when a Codex session reports in, hive finds its TUI through the window it
opened for it, or as the only Codex TUI in that folder; two Codex TUIs in one folder
stay apart until one of them ends. Codex exports `CODEX_THREAD_ID` to the commands
it runs, so what a Codex session spawns links to it even through the daemon. Codex's
own spawned agents are separate threads; they appear as subagents under the thread
that spawned them.

### Sessions from before hive

`hive sync` (run by `hive ls`) imports each tool's own history: Claude transcripts
(`~/.claude/projects`), opencode's database, Copilot CLI's session store
(`~/.copilot/session-store.db`, opened read-only) plus session-state event logs, and
Codex's rollouts (`~/.codex/sessions`) plus its state database (`~/.codex/state_*.sqlite`,
opened read-only) for thread names and spawned-agent links. Only what changed since
the last sync is read, so it takes milliseconds after the first run.

Past spawns are found in the shell commands every session ran, in any direction:
`opencode run --title X` inside Claude, `claude -p` inside opencode, `codex exec`
inside either, and so on. A command is matched to the session it started by title
first (loop titles like `review-p$i` match every run), then by start time and folder;
a command continuing a session by ID (`opencode run -s ses_…`, `codex resume <id>`)
only links sessions nothing else claims.

A running agent that never reported in (started before `hive install`) is matched to
its transcript when that is unambiguous: it is the only such process of its tool in
its folder, and exactly one session there was active since it started.

## Adding or changing a tool

A tool hive doesn't ship with is a few lines of config, and so is a change to a
built-in one:

```toml
# ~/.config/hive/config.toml ($XDG_CONFIG_HOME/hive/config.toml; HIVE_CONFIG overrides)
[[agent]]
name     = "aider"
new      = ["aider", "--message", "{prompt}"]
resume   = ["aider", "--restore-chat-history"]
headless = ["--message"]

[[agent]]  # a built-in tool: only the fields given change
name = "opencode"
new  = ["opencode", "-m", "deepseek/deepseek-v4-pro", "--prompt", "{prompt}"]
```

`new` and `resume` are the commands that start and reopen a session: `{prompt}` is the
first message, `{session}` an ID hive picks for it, `{id}` the session to reopen, and a
flag right before an empty placeholder is dropped with it. `process` names the tool's
processes (by default, the program `new` runs); `headless`, `title_flags`,
`session_flags` and `parent_env` help link past spawns, as in the built-in adapters.
A new tool reports through `hive hook <name>`, below. A config with mistakes is
ignored, and `hive doctor` says what is wrong with it.

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
