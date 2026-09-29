package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent/agenttest"
	"github.com/sadrishehu/hive/internal/store"
)

func TestPurgeRunsOpencodeSessionDelete(t *testing.T) {
	log := agenttest.FakeTool(t, "opencode", `case "$3" in
	ses_gone) printf '\033[91mError: \033[0mSession not found: %s\n' "$3" >&2; exit 1;;
	ses_busy) echo "Error: database is locked" >&2; exit 1;;
esac
echo "Session $3 deleted"`)
	a := New()
	ctx := context.Background()
	if err := a.Purge(ctx, store.Session{NativeID: "ses_ok"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: "ses_gone"}); err != nil {
		t.Errorf("a session already gone: %v", err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: "ses_busy"}); err == nil || !strings.HasSuffix(err.Error(), "database is locked") {
		t.Errorf("err = %v", err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: "../ses_x"}); err == nil {
		t.Error("purged an ID that isn't one")
	}
	if want := "session delete ses_ok\nsession delete ses_gone\nsession delete ses_busy"; log() != want {
		t.Errorf("opencode ran:\n%s\nwant:\n%s", log(), want)
	}
}
