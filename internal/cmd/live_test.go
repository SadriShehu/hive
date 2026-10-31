package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/tmux"
)

// A live test runs hive and the stand-in agents of docs/demo/agent as the
// processes they are, hooks and all, against a scratch home, database and
// config, and a tmux server of its own. The agents' windows take their
// environment from that server, so their hooks reach the scratch database
// too. Nothing of the user's is read or touched, and nothing is spent.
//
// It builds hive and the agent, and takes about half a minute; -short skips it.

// agentTurn is how long a stand-in agent works on each message.
const agentTurn = 3 * time.Second

type liveHive struct {
	t      *testing.T
	dir    string
	work   string // the folder the team works in
	socket string // the private tmux server
	env    []string
}

// note is one line the stand-in agents write to HIVE_DEMO_LOG.
type note struct {
	At      int64  `json:"at"`
	Tool    string `json:"tool"`
	ID      string `json:"id"`
	PID     int    `json:"pid"`
	Event   string `json:"event"` // start, got, ran, idle, end
	Text    string `json:"text"`
	Working bool   `json:"working"` // it came while the agent was working
}

func (n note) session() string { return n.Tool + ":" + n.ID }

type liveMember struct {
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	State     string `json:"state"`
}

func startLive(t *testing.T) *liveHive {
	t.Helper()
	if testing.Short() {
		t.Skip("a live test: it runs hive and stand-in agents in tmux")
	}
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go isn't on PATH, to build hive with")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "hive-live-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir) // the agents report the folder they find themselves in
	bin, home := filepath.Join(dir, "bin"), filepath.Join(dir, "home")
	h := &liveHive{t: t, dir: dir, work: filepath.Join(home, "work"), socket: fmt.Sprintf("hive-test-%d", os.Getpid())}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(h.work, 0o755); err != nil {
		t.Fatal(err)
	}
	for pkg, out := range map[string]string{".": "hive", "./docs/demo/agent": "hive-demo-agent"} {
		build := exec.Command(goBin, "build", "-o", filepath.Join(bin, out), pkg)
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", pkg, err, out)
		}
	}
	for _, tool := range []string{"claude", "codex"} {
		if err := os.Symlink("hive-demo-agent", filepath.Join(bin, "hive-demo-"+tool)); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing of the agent or the tmux this test may run in: no TMUX, which
	// beats -L; no HIVE_PARENT or CLAUDE_CODE_SESSION_ID, which would link
	// the members to it; no tool's own folders.
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "HOME", k == "PATH", strings.HasPrefix(k, "TMUX"), strings.HasPrefix(k, "HIVE_"),
			strings.HasPrefix(k, "CLAUDE"), strings.HasPrefix(k, "CODEX_"), strings.HasPrefix(k, "COPILOT_"),
			strings.HasPrefix(k, "OPENCODE"), strings.HasPrefix(k, "XDG_"):
			continue
		}
		h.env = append(h.env, kv)
	}
	h.env = append(h.env, "HOME="+home, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HIVE_HOME="+filepath.Join(dir, "hive"), "HIVE_CONFIG="+filepath.Join(root, "docs", "demo", "config.toml"),
		"HIVE_TMUX_SOCKET="+h.socket, "HIVE_DEMO_LOG="+filepath.Join(dir, "agents.log"),
		"HIVE_DEMO_TURN="+agentTurn.String())

	if _, err := h.tmux("-f", "/dev/null", "new-session", "-d", "-s", "base", "-x", "200", "-y", "50"); err != nil {
		t.Fatal(err)
	}
	socketPath, _ := h.tmux("display-message", "-p", "#{socket_path}")
	t.Cleanup(func() {
		h.tmux("kill-server")
		if strings.Contains(socketPath, h.socket) {
			os.Remove(socketPath)
		}
		h.settle()
	})
	if testing.Verbose() { // what happened, read before the server goes
		t.Cleanup(func() { t.Log(h.dump()) })
	}
	return h
}

// tmux runs a command on the test's own tmux server, never the user's.
func (h *liveHive) tmux(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-L", h.socket}, args...)...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// settle waits for what the agents' hooks started, like `hive team
// deliver`, to finish, so nothing writes to the scratch folder as it goes.
func (h *liveHive) settle() {
	for range 50 {
		out, _ := exec.Command("ps", "-A", "-ww", "-o", "args=").Output()
		if !strings.Contains(string(out), filepath.Join(h.dir, "bin")) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// hive runs hive and returns what it printed.
func (h *liveHive) hive(args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(h.dir, "bin", "hive"), args...)
	cmd.Env, cmd.Dir = h.env, h.work
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// must runs hive and fails the test if it fails.
func (h *liveHive) must(args ...string) (stdout, stderr string) {
	h.t.Helper()
	stdout, stderr, err := h.hive(args...)
	if err != nil {
		h.t.Fatalf("hive %s: %v\n%s%s\n%s", strings.Join(args, " "), err, stdout, stderr, h.dump())
	}
	return stdout, stderr
}

// members returns a team's members by role, as `hive team ls` sees them.
func (h *liveHive) members(team string) map[string]liveMember {
	h.t.Helper()
	out, _ := h.must("team", "ls", "--json")
	var teams []struct {
		Name    string       `json:"name"`
		Members []liveMember `json:"members"`
	}
	if err := json.Unmarshal([]byte(out), &teams); err != nil {
		h.t.Fatalf("team ls --json: %v\n%s", err, out)
	}
	ms := map[string]liveMember{}
	for _, tm := range teams {
		if tm.Name == team {
			for _, m := range tm.Members {
				ms[m.Role] = m
			}
		}
	}
	return ms
}

// notes reads what the agents wrote down so far.
func (h *liveHive) notes() []note {
	f, err := os.Open(filepath.Join(h.dir, "agents.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []note
	in := bufio.NewScanner(f)
	in.Buffer(nil, 1<<20)
	for in.Scan() {
		var n note
		if json.Unmarshal(in.Bytes(), &n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// got returns the first message session got that matches.
func (h *liveHive) got(session string, match func(text string) bool) (note, bool) {
	for _, n := range h.notes() {
		if n.Event == "got" && n.session() == session && match(n.Text) {
			return n, true
		}
	}
	return note{}, false
}

// waitGot waits for session to get text, typed into its pane or as its
// first prompt.
func (h *liveHive) waitGot(session, text string) note {
	h.t.Helper()
	var n note
	h.waitFor(session+" to get "+text, 20*time.Second, func() bool {
		var ok bool
		n, ok = h.got(session, func(t string) bool { return t == text })
		return ok
	})
	return n
}

func (h *liveHive) waitFor(what string, timeout time.Duration, ok func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(timeout); !ok(); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			h.t.Fatalf("waited %s for %s\n%s", timeout, what, h.dump())
		}
	}
}

// dump is what helps when the test fails: what the agents did, the channel,
// and hive's log.
func (h *liveHive) dump() string {
	var b strings.Builder
	b.WriteString("agents:\n")
	for _, n := range h.notes() {
		fmt.Fprintf(&b, "  %d %s %d %s working=%v %.80q\n", n.At%100_000, n.session(), n.PID, n.Event, n.Working, n.Text)
	}
	if out, _, err := h.hive("team", "log", "live", "-n", "50"); err == nil {
		b.WriteString("channel:\n" + out)
	}
	if log, err := os.ReadFile(filepath.Join(h.dir, "hive", "hive.log")); err == nil {
		b.WriteString("hive.log:\n" + string(log))
	}
	return b.String()
}

// next returns session's first note of event after at.
func (h *liveHive) next(session, event string, at int64) (note, bool) {
	for _, n := range h.notes() {
		if n.session() == session && n.Event == event && n.At > at {
			return n, true
		}
	}
	return note{}, false
}

// waitIdle waits for session to end the turn it took at at.
func (h *liveHive) waitIdle(session string, at int64) note {
	h.t.Helper()
	var n note
	h.waitFor(session+" to end its turn", 4*agentTurn, func() bool {
		var ok bool
		n, ok = h.next(session, "idle", at)
		return ok
	})
	return n
}

func (h *liveHive) channel() string {
	out, _ := h.must("team", "log", "live", "-n", "100")
	return out
}

func TestTeamMailboxLive(t *testing.T) {
	h := startLive(t)
	from := func(who, text string) string { return "[live · from " + who + "] " + text }

	started, _ := h.must("team", "new", "live", "--goal", "try the mailbox", "--lead", "none",
		"--member", "backend=claude", "--member", "frontend=codex", "--cwd", h.work, "--wait", "--timeout", "45s")
	var backend, frontend liveMember
	h.waitFor("both members to read their briefs and go idle", 30*time.Second, func() bool {
		ms := h.members("live")
		backend, frontend = ms["backend"], ms["frontend"]
		return backend.State == "idle" && frontend.State == "idle"
	})
	for _, m := range []liveMember{backend, frontend} {
		if !strings.Contains(started, m.SessionID) {
			t.Errorf("team new --wait didn't print %s's ID %s:\n%s", m.Role, m.SessionID, started)
		}
		if _, ok := h.got(m.SessionID, func(t string) bool { return strings.HasPrefix(t, "You are "+m.Role+" on the hive team live") }); !ok {
			t.Fatalf("%s (%s) didn't start with its brief\n%s", m.Role, m.SessionID, h.dump())
		}
	}

	// An idle member: the message is typed at once.
	if _, said := h.must("team", "say", "backend", "hello backend"); !strings.Contains(said, "backend: sent") {
		t.Fatalf("say to an idle member: %q, want it sent", said)
	}
	first := h.waitGot(backend.SessionID, from("you", "hello backend"))

	// A working member: what is written to it waits for its turn to end, and
	// the `hive team deliver` its idle hook starts types it all as one paste.
	h.waitFor("backend to work on the message", 10*time.Second, func() bool {
		return h.members("live")["backend"].State == "working"
	})
	if _, said := h.must("team", "say", "backend", "this waits for your turn"); !strings.Contains(said, "backend: queued; it is working") {
		t.Fatalf("say to a working member: %q, want it queued", said)
	}
	h.must("team", "post", "@backend and so does this")
	idle := h.waitIdle(backend.SessionID, first.At)
	both := h.waitGot(backend.SessionID, from("you", "this waits for your turn")+"\n"+from("you", "@backend and so does this"))
	if both.Working || both.At < idle.At {
		t.Fatalf("the mail came before backend's turn ended\n%s", h.dump())
	}
	if log, _ := os.ReadFile(filepath.Join(h.dir, "hive", "hive.log")); !strings.Contains(string(log), "team deliver "+backend.SessionID+": sent") {
		t.Fatalf("no `hive team deliver` typed it\n%s", h.dump())
	}
	// It counts as given to backend: `hive wait` waits for the answer.
	if out, _ := h.must("wait", backend.SessionID, "--timeout", "20s"); !strings.Contains(out, backend.SessionID+" idle") {
		t.Fatalf("hive wait = %q", out)
	}
	if _, ok := h.next(backend.SessionID, "idle", both.At); !ok {
		t.Fatalf("hive wait returned before backend answered its mail\n%s", h.dump())
	}

	// A member writes to another from its shell, as itself.
	h.must("team", "say", "backend", "$ hive team say frontend the API is up")
	h.waitGot(frontend.SessionID, from("backend", "the API is up"))

	// A member starts a team of its own and leads it. Its mail from both
	// teams comes in one paste, and a command it runs has to name the team.
	h.waitFor("frontend to finish", 15*time.Second, func() bool { return h.members("live")["frontend"].State == "idle" })
	h.must("team", "say", "frontend", "$ hive team new side --lead self --member helper=claude --cwd "+h.work)
	h.waitFor("side's helper to read its brief and go idle", 30*time.Second, func() bool {
		ms := h.members("side")
		return ms["lead"].SessionID == frontend.SessionID && ms["helper"].State == "idle"
	})
	h.waitFor("frontend to finish", 15*time.Second, func() bool { return h.members("live")["frontend"].State == "idle" })
	h.must("send", frontend.SessionID, "$ hive team say backend which team is this")
	asked := h.waitGot(frontend.SessionID, "$ hive team say backend which team is this")
	h.waitFor("frontend to work", 10*time.Second, func() bool { return h.members("live")["frontend"].State == "working" })
	h.must("team", "say", "--team", "live", "frontend", "from live")
	h.must("team", "say", "--team", "side", "lead", "from side")
	h.waitGot(frontend.SessionID, "[side · from you] from side\n"+from("you", "from live"))
	if ran, ok := h.next(frontend.SessionID, "ran", asked.At); !ok || !strings.Contains(ran.Text, "more than one team") {
		t.Fatalf("a member on two teams wrote without naming one: %+v\n%s", ran, h.dump())
	}

	// A paused team holds its mail, even when the member goes idle; resuming
	// delivers it.
	h.waitFor("frontend to finish", 15*time.Second, func() bool { return h.members("live")["frontend"].State == "idle" })
	h.must("team", "pause", "live")
	if _, said := h.must("team", "say", "--team", "live", "frontend", "after the pause"); !strings.Contains(said, "frontend: queued; the team is paused") {
		t.Fatalf("say on a paused team: %q, want it queued", said)
	}
	h.must("send", frontend.SessionID, "ping")
	ping := h.waitGot(frontend.SessionID, "ping")
	h.waitIdle(frontend.SessionID, ping.At)
	time.Sleep(2 * time.Second) // what a delivery after that idle would take
	if _, ok := h.got(frontend.SessionID, func(t string) bool { return t == from("you", "after the pause") }); ok {
		t.Fatalf("a paused team's mail was delivered\n%s", h.dump())
	}
	h.must("team", "resume", "live")
	h.waitGot(frontend.SessionID, from("you", "after the pause"))

	// A member that has ended is reopened with its mail as its first prompt,
	// and the channel says so.
	h.waitFor("backend to finish its work", 15*time.Second, func() bool {
		return h.members("live")["backend"].State == "idle"
	})
	h.must("kill", backend.SessionID)
	h.waitFor("backend to end", 10*time.Second, func() bool { return h.members("live")["backend"].State == "ended" })
	if !strings.Contains(h.channel(), "backend stopped") {
		t.Fatalf("the channel doesn't say backend stopped\n%s", h.dump())
	}
	if _, said := h.must("team", "say", "--team", "live", "backend", "welcome back"); !strings.Contains(said, "backend: reopened") {
		t.Fatalf("say to a member that ended: %q, want it reopened", said)
	}
	back := h.waitGot(backend.SessionID, from("you", "welcome back"))
	if back.PID == first.PID {
		t.Fatalf("backend got the message in its old process\n%s", h.dump())
	}
	h.waitFor("backend to be back on the team", 15*time.Second, func() bool {
		m := h.members("live")["backend"]
		return m.SessionID == backend.SessionID && (m.State == "working" || m.State == "idle")
	})
	if log := h.channel(); strings.Count(log, "backend stopped") != 1 || !strings.Contains(log, "backend reopened") {
		t.Fatalf("the channel should say once that backend stopped, then that it was reopened\n%s", h.dump())
	}
}
