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
	// No opencode binary: install falls back to the opencode 1 plugin.
	a := &Adapter{ConfigDir: dir, OpenCodeBin: filepath.Join(t.TempDir(), "no-opencode")}
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

// fakeOpenCode writes an executable that prints version, like `opencode --version`.
func fakeOpenCode(t *testing.T, version string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestInstallPicksTemplateForVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string // "" points the adapter at a missing binary
		want    string
		msg     string
	}{
		{name: "opencode2", version: "opencode v2.2.7", want: "export default {", msg: "for opencode 2"},
		{name: "opencode1", version: "opencode v1.18.9", want: "export const HivePlugin", msg: "for opencode 1"},
		{name: "unknown", version: "opencode vx.y.z", want: "export const HivePlugin", msg: ""},
		{name: "missing", want: "export const HivePlugin", msg: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "no-opencode")
			if c.version != "" {
				bin = fakeOpenCode(t, c.version)
			}
			a := &Adapter{ConfigDir: t.TempDir(), OpenCodeBin: bin}
			msg, err := a.Install("/bin/hive")
			if err != nil {
				t.Fatal(err)
			}
			if c.msg != "" && !strings.Contains(msg, c.msg) {
				t.Errorf("install = %q, want %q", msg, c.msg)
			}
			data, err := os.ReadFile(filepath.Join(a.ConfigDir, "plugin", "hive.js"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), c.want) {
				t.Errorf("plugin for %q lacks %q:\n%s", c.version, c.want, data)
			}
		})
	}
}

// Both templates must start with the marker line so Installed and Uninstall
// recognize a plugin hive wrote, whichever opencode version installed it.
func TestPluginTemplatesShareMarker(t *testing.T) {
	for name, src := range map[string]string{"opencode1": pluginSource, "opencode2": pluginSourceV2} {
		if first := strings.SplitN(src, "\n", 2)[0]; first != marker {
			t.Errorf("the %s plugin starts with %q, marker is %q", name, first, marker)
		}
	}
}

func TestInstallLeavesForeignFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin", "hive.js")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("export const Mine = async () => ({})\n"), 0o644)
	a := &Adapter{ConfigDir: dir}
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

func TestPluginMarksAnAbortedSessionIdle(t *testing.T) {
	for _, want := range []string{`case "session.error"`, "MessageAbortedError", `status(p.sessionID, "idle")`} {
		if !strings.Contains(pluginSource, want) {
			t.Errorf("the opencode 1 plugin does not handle an aborted turn: it lacks %q", want)
		}
	}
	for _, want := range []string{`case "session.execution.interrupted"`, `status(p.sessionID, "idle")`} {
		if !strings.Contains(pluginSourceV2, want) {
			t.Errorf("the opencode 2 plugin does not handle an aborted turn: it lacks %q", want)
		}
	}
}
