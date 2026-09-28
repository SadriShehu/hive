package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/agent/claude"
	"github.com/sadrishehu/hive/internal/agent/opencode"
	"github.com/sadrishehu/hive/internal/proc"
	"github.com/sadrishehu/hive/internal/store"
)

// history is a machine's past: Claude transcripts and an opencode database.
type history struct {
	t        *testing.T
	projects string
	db       *sql.DB
	dbPath   string
}

func newHistory(t *testing.T) *history {
	t.Helper()
	h := &history{t: t, projects: t.TempDir(), dbPath: filepath.Join(t.TempDir(), "opencode.db")}
	db, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h.db = db
	h.exec(`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL,
			title TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
	return h
}

func (h *history) exec(q string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		h.t.Fatal(err)
	}
}

func ts(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

// claude writes a transcript: a prompt at start, then one Bash call per command.
func (h *history) claude(id, cwd string, start int64, commands ...string) {
	h.t.Helper()
	lines := []string{fmt.Sprintf(`{"type":"user","message":{"content":"work"},"timestamp":%q,"cwd":%q,"entrypoint":"cli"}`, ts(start), cwd)}
	for i, c := range commands {
		cmd, _ := jsonString(c)
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":%s}}]},"timestamp":%q,"cwd":%q}`,
			cmd, ts(start+int64(i+1)*1000), cwd))
	}
	dir := filepath.Join(h.projects, "p")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// opencode records a session, with bash commands one second apart.
func (h *history) opencode(id, parent, cwd, title string, created int64, commands ...string) {
	h.t.Helper()
	var p any
	if parent != "" {
		p = parent
	}
	h.exec(`INSERT INTO session VALUES (?, ?, ?, ?, ?, ?)`, id, p, cwd, title, created, created+60_000)
	for i, c := range commands {
		cmd, _ := jsonString(c)
		h.exec(`INSERT INTO part VALUES (?, 'm', ?, ?, ?, ?)`, fmt.Sprintf("%s-%d", id, i), id,
			created+int64(i+1)*1000, created+int64(i+1)*1000,
			`{"type":"tool","tool":"bash","state":{"input":{"command":`+cmd+`}}}`)
	}
}

func jsonString(s string) (string, error) {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`, nil
}

func (h *history) sync(t *testing.T) (*Tracker, SyncResult) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	adapters := []agent.Adapter{&claude.Adapter{ProjectsDir: h.projects}, &opencode.Adapter{DBPath: h.dbPath}}
	tr := New(st, adapters, &fakeWorld{procs: proc.Table{}, now: 400 * minute})
	res, err := tr.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tr, res
}

func parentOf(t *testing.T, tr *Tracker, id string) string {
	t.Helper()
	return get(t, tr, id).ParentID
}

const minute = 60_000

func TestSyncLinksPastSpawnsInBothDirections(t *testing.T) {
	h := newHistory(t)
	// Claude → opencode, by title, even when the run starts minutes later.
	h.claude("c1", "/src/app", 100*minute,
		"cd backend && opencode run -m deepseek/deepseek-flash --title impl-be 'build it'")
	h.opencode("ses_be", "", "/src/app/backend", "impl-be", 104*minute)
	// opencode → Claude, by time and folder.
	h.opencode("ses_oc", "", "/src/web", "web work", 200*minute, "claude -p 'review the diff'")
	h.claude("c2", "/src/web", 200*minute+2000)
	// opencode's own subagent keeps its parent.
	h.opencode("ses_sub", "ses_oc", "/src/web", "explore", 200*minute+500)
	// An unrelated opencode session in the same window stays a root.
	h.opencode("ses_other", "", "/elsewhere", "other", 200*minute+3000)

	tr, res := h.sync(t)
	if res.Imported["claude"] != 2 || res.Imported["opencode"] != 4 || res.Linked != 2 {
		t.Fatalf("result = %+v", res)
	}
	for id, want := range map[string]string{
		"opencode:ses_be":    "claude:c1",
		"claude:c2":          "opencode:ses_oc",
		"opencode:ses_sub":   "opencode:ses_oc",
		"opencode:ses_other": "",
	} {
		if got := parentOf(t, tr, id); got != want {
			t.Errorf("%s parent = %q, want %q", id, got, want)
		}
	}
}

func TestSyncLoopTitlesLinkEveryRun(t *testing.T) {
	h := newHistory(t)
	h.claude("c1", "/src", 100*minute, `for i in 1 2 3; do opencode run --title review-p$i "review part $i" & done; wait`)
	for i := 1; i <= 3; i++ {
		h.opencode(fmt.Sprintf("ses_%d", i), "", "/src", fmt.Sprintf("review-p%d", i), 100*minute+int64(i)*3000)
	}
	h.opencode("ses_x", "", "/src", "review-summary", 100*minute+5000) // similar, but not from the loop

	tr, _ := h.sync(t)
	for i := 1; i <= 3; i++ {
		if got := parentOf(t, tr, fmt.Sprintf("opencode:ses_%d", i)); got != "claude:c1" {
			t.Errorf("ses_%d parent = %q, want claude:c1", i, got)
		}
	}
	if got := parentOf(t, tr, "opencode:ses_x"); got != "" {
		t.Errorf("ses_x parent = %q, want none", got)
	}
	if k := get(t, tr, "opencode:ses_1").Kind; k != store.KindHeadless {
		t.Errorf("ses_1 kind = %q, want headless: it came from `opencode run`", k)
	}
}

func TestSyncPreciseHintsWin(t *testing.T) {
	h := newHistory(t)
	// c1 starts an untitled run and, a moment later, a titled one in the
	// same folder. The untitled one failed to start; the titled one is
	// ses_a, which c2 later continues by ID.
	h.claude("c1", "/src", 100*minute, "opencode run 'first try'", "opencode run --title the-real-one 'go'")
	h.opencode("ses_a", "", "/src", "the-real-one", 100*minute+3000)
	h.claude("c2", "/src", 300*minute, "opencode run -s ses_a 'continue'")

	tr, _ := h.sync(t)
	if got := parentOf(t, tr, "opencode:ses_a"); got != "claude:c1" {
		t.Errorf("ses_a parent = %q, want claude:c1 (the continuation must not take it)", got)
	}
	if hints, _ := tr.Store.OpenHints(); len(hints) != 1 || hints[0].Title != "" || hints[0].NativeID != "" {
		t.Errorf("open hints = %+v, want only the untitled try that started nothing", hints)
	}
}
