# hive

Track every coding-agent session (Claude Code, opencode, and later others) as one tree:
who spawned whom, what each agent is doing right now, and a key press to jump into
or command any of them. Each agent runs its own real TUI in a tmux window; hive never
wraps or reimplements an agent.

Design doc: https://claude.ai/code/artifact/5df7a9f0-da62-4b84-a527-eec21528bf63

## Status

Phase 1 of 5 (tracking) is done: live sessions and spawn links show in `hive ls`.
History import, the TUI and the agent-facing commands come next.

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

## Use

```sh
hive ls           # trees with anything running or active in the last 24h
hive ls --all     # every session ever recorded
hive ls --live    # only running sessions and their ancestors
hive ls --json    # flat list in tree order, for scripts and agents
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
and log elsewhere.
