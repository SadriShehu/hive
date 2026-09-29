package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent/agenttest"
	"github.com/sadrishehu/hive/internal/store"
)

func TestPurgeRunsCodexDelete(t *testing.T) {
	home := t.TempDir()
	const ok, gone, busy = "01a0ec48-f83c-7390-b7b8-86abb3ecd76c", "01a0ec48-0000-7000-8000-000000000000", "01a0ec48-1111-7000-8000-000000000000"
	rollout := func(id string) string { return "sessions/2026/09/29/rollout-2026-09-29T10-30-00-" + id + ".jsonl" }
	agenttest.Files(t, home, rollout(ok), rollout(busy))
	log := agenttest.FakeTool(t, "codex", `echo "home=$CODEX_HOME" >> "$(dirname "$0")/codex.log"
if [ "$3" != "`+ok+`" ]; then echo "Error: failed to delete session" >&2; exit 1; fi
rm "$CODEX_HOME"/sessions/*/*/*/rollout-*-"$3".jsonl
echo "Deleted session $3."`)
	a := &Adapter{HomeDir: home}
	ctx := context.Background()
	if err := a.Purge(ctx, store.Session{NativeID: ok}); err != nil {
		t.Fatal(err)
	}
	if left := agenttest.Exists(home, rollout(ok)); len(left) > 0 {
		t.Errorf("left %v", left)
	}
	if err := a.Purge(ctx, store.Session{NativeID: gone}); err != nil {
		t.Errorf("a thread already gone: %v", err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: busy}); err == nil || !strings.Contains(err.Error(), "failed to delete session") {
		t.Errorf("a thread codex couldn't delete: err = %v", err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: "not-a-uuid"}); err == nil {
		t.Error("purged an ID that isn't a thread's")
	}
	if want := "delete --force " + ok + "\nhome=" + home; !strings.HasPrefix(log(), want) {
		t.Errorf("codex ran:\n%s\nwant it to start with:\n%s", log(), want)
	}
}
