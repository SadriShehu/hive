#!/usr/bin/env bash
# Records docs/demo.gif from docs/demo.tape. The agents are stand-ins
# (agent/), and hive, tmux and the agents run with a scratch home, database
# and tmux server, so nothing real is read or touched and nothing is spent.
# Needs Go, tmux and vhs.
#
#   docs/demo/record.sh         record the gif
#   docs/demo/record.sh --try   set the demo up and leave it running instead
set -euo pipefail
cd "$(dirname "$0")/../.."
sock=hivedemo
demo=$(mktemp -d /tmp/hive-demo.XXXXXX)
cleanup() {
	tmux -L "$sock" kill-server 2>/dev/null || true
	rm -rf "$demo"
}
trap cleanup EXIT
tmux -L "$sock" kill-server 2>/dev/null || true

go build -o "$demo/bin/hive" .
go build -o "$demo/bin/hive-demo-agent" ./docs/demo/agent
# Name this run's stand-ins apart from those a test may run beside it, so the
# tree here shows this demo only. The live test keeps the plain names.
agent_prefix=hive-demo-rec-
for tool in claude opencode codex copilot; do
	ln -s hive-demo-agent "$demo/bin/$agent_prefix$tool"
done
sed "s|hive-demo-|$agent_prefix|g" docs/demo/config.toml > "$demo/config.toml"
home=$demo/home
mkdir -p "$home"/code/{shop/{api,e2e,web,.git},infra,docs-site,mobile}

# scratch runs a command as if in a fresh account: only the scratch home,
# and no trace of the agent or tmux this script may itself run in.
scratch() {
	env -u TMUX -u TMUX_PANE -u HIVE_PARENT -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID \
		-u CLAUDE_CONFIG_DIR -u CODEX_HOME -u COPILOT_HOME -u OPENCODE_CONFIG_DIR \
		-u XDG_CONFIG_HOME -u XDG_DATA_HOME \
		HOME="$home" PATH="$demo/bin:$PATH" HIVE_HOME="$demo/hive" \
		HIVE_DEMO_AGENT_PREFIX="$agent_prefix" \
		HIVE_CONFIG="$demo/config.toml" HIVE_TMUX_SOCKET="$sock" "$@"
}

# Agents open in the "hive" session, as they would outside tmux. The tree
# takes its first window once they are up, so it opens on the newest.
shop=$home/code/shop
scratch tmux -L "$sock" -f docs/demo/tmux.conf new-session -d -s hive -n hive -x 160 -y 44 -c "$shop" sleep 600
scratch tmux -L "$sock" new-window -d -c "$home/code/infra" "${agent_prefix}opencode run 'bump Go to 1.25 in the Dockerfiles'"
scratch hive new opencode --parent none --cwd "$home/code/docs-site" -p "document the new checkout flow" >/dev/null
sleep 1
scratch hive new copilot --parent none --cwd "$shop" -p "fix the flaky login test" >/dev/null
sleep 2
scratch hive new claude --parent none --cwd "$shop" -p "redesign checkout as one page and keep saved cards" >/dev/null

# Claude starts opencode, which starts a Codex run: wait for that.
for _ in $(seq 60); do
	scratch hive ls 2>/dev/null | grep -q codex && break
	sleep 0.5
done

# A small team starts before the tree attaches, so its node and members are
# already there; the board and channel have something to show too.
scratch hive team new checkout --goal "make checkout one page and keep saved cards" \
	--lead claude --member backend=codex --member frontend=opencode \
	--cwd "$shop" --wait --timeout 30s >/dev/null
scratch hive team task add --team checkout "saved cards API" --owner backend >/dev/null
scratch hive team task add --team checkout "one-page layout and styles" --owner frontend >/dev/null
scratch hive team task add --team checkout "review the checkout flow" --owner lead --after 1,2 >/dev/null
scratch hive team post --team checkout "Plan is on the board: the API and the page first, review after." >/dev/null
scratch tmux -L "$sock" respawn-pane -k -t hive:hive -c "$shop" hive ui

if [[ ${1:-} == --try ]]; then
	trap - EXIT
	echo "tmux -L $sock attach    # to look; when done:"
	echo "tmux -L $sock kill-server; rm -rf $demo"
	exit
fi
env -u TMUX -u TMUX_PANE vhs docs/demo.tape
