package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
)

func TestInstallRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := NewWithConfigDir(dir)
	if _, err := a.Install("/Users/me/go/bin/hive"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plugin", "hive.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `const HIVE = "/Users/me/go/bin/hive"`) || !a.Installed() {
		t.Fatalf("plugin not pointed at hive:\n%s", data)
	}
	if msg, _ := a.Install("/Users/me/go/bin/hive"); !strings.Contains(msg, "already installed") {
		t.Errorf("second install = %q", msg)
	}
	if _, err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin", "hive.js")); !os.IsNotExist(err) {
		t.Error("plugin still there after uninstall")
	}
}

func TestInstallLeavesForeignFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin", "hive.js")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("export const Mine = async () => ({})\n"), 0o644)
	a := NewWithConfigDir(dir)
	if _, err := a.Install("/bin/hive"); err == nil {
		t.Error("overwrote a plugin hive did not write")
	}
	if msg, _ := a.Uninstall(); !strings.Contains(msg, "not installed") {
		t.Errorf("uninstall = %q", msg)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "Mine") {
		t.Error("foreign plugin was modified")
	}
}

func TestParseHook(t *testing.T) {
	payload := `{"event":"start","session_id":"ses_1","parent_id":"opencode:ses_0","internal":true,"title":"t","cwd":"/w","pid":42,"at":7}
{"event":"busy","session_id":"ses_1","pid":42,"at":8}`
	evs, err := New().ParseHook([]byte(payload), nil, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := agent.Event{Tool: "opencode", SessionID: "ses_1", Type: agent.Start, At: 7, PID: 42,
		ParentID: "opencode:ses_0", Internal: true, Title: "t", Cwd: "/w"}
	if len(evs) != 2 || evs[0] != want || evs[1].Type != agent.Busy {
		t.Fatalf("events = %+v", evs)
	}
	if _, err := New().ParseHook([]byte(`{"event":"explode","session_id":"x"}`), nil, os.Getenv); err == nil {
		t.Error("unknown event accepted")
	}
}
