package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
)

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func find(t *testing.T, all []agent.Adapter, name string) agent.Adapter {
	t.Helper()
	for _, a := range all {
		if a.Spec().Name == name {
			return a
		}
	}
	t.Fatalf("no %s adapter", name)
	return nil
}

func TestLoadWithoutConfig(t *testing.T) {
	all, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || len(all) != len(Builtin()) {
		t.Fatalf("Load(missing) = %d adapters, %v", len(all), err)
	}
}

func TestLoadAddsAndOverrides(t *testing.T) {
	all, err := Load(writeConfig(t, `
[[agent]]
name     = "aider"
new      = ["aider", "--message", "{prompt}"]
resume   = ["aider", "--restore-chat-history"]
headless = ["--message"]

[[agent]]
name = "opencode"
new  = ["opencode", "-m", "deepseek/deepseek-v4-pro", "--prompt", "{prompt}"]
`))
	if err != nil {
		t.Fatal(err)
	}

	aider := find(t, all, "aider")
	if spec := aider.Spec(); !slices.Equal(spec.Process, []string{"aider"}) || spec.New[0] != "aider" {
		t.Errorf("aider spec = %+v", spec)
	}
	if !aider.Installed() {
		t.Error("a config-only tool has nothing to install, so it counts as installed")
	}
	evs, err := aider.ParseHook([]byte(`{"event":"start","session_id":"a1","cwd":"/src"}`), nil, os.Getenv)
	if err != nil || len(evs) != 1 || evs[0].Tool != "aider" || evs[0].SessionID != "a1" || evs[0].Type != agent.Start {
		t.Errorf("aider ParseHook = %+v, %v", evs, err)
	}

	opencode := find(t, all, "opencode")
	spec := opencode.Spec()
	if spec.New[2] != "deepseek/deepseek-v4-pro" || len(spec.Resume) == 0 || len(spec.Process) == 0 {
		t.Errorf("opencode override lost built-in fields: %+v", spec)
	}
	// The built-in's optional abilities survive the override.
	if _, ok := opencode.(agent.Importer); !ok {
		t.Error("overridden opencode is no longer an Importer")
	}
	if _, ok := opencode.(agent.Tailer); !ok {
		t.Error("overridden opencode is no longer a Tailer")
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, text := range map[string]string{
		"syntax":    `[[agent]` + "\n",
		"unknown":   "[[agent]]\nname = \"x\"\nnwe = [\"x\"]\n",
		"bad name":  "[[agent]]\nname = \"My Tool\"\n",
		"no name":   "[[agent]]\nnew = [\"x\"]\n",
		"duplicate": "[[agent]]\nname = \"x\"\n[[agent]]\nname = \"x\"\n",
	} {
		all, err := Load(writeConfig(t, text))
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(all) != len(Builtin()) {
			t.Errorf("%s: a bad config changed the adapters", name)
		}
	}
}

func TestAlerts(t *testing.T) {
	alerts, err := LoadAlerts(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || alerts != (Alerts{Tmux: true}) {
		t.Fatalf("without config: %+v, %v; want the tmux message only", alerts, err)
	}
	alerts, err = LoadAlerts(writeConfig(t, "[alerts]\ndesktop = true\n"))
	if err != nil || alerts != (Alerts{Tmux: true, Desktop: true}) {
		t.Fatalf("desktop on: %+v, %v", alerts, err)
	}
	alerts, err = LoadAlerts(writeConfig(t, "[alerts]\ntmux = false\n"))
	if err != nil || alerts != (Alerts{}) {
		t.Fatalf("tmux off: %+v, %v", alerts, err)
	}

	// A mistake anywhere leaves the defaults, and says what it is.
	alerts, err = LoadAlerts(writeConfig(t, "[alerts]\nsound = true\n"))
	if err == nil || alerts != (Alerts{Tmux: true}) {
		t.Fatalf("unknown setting: %+v, %v", alerts, err)
	}
	if _, err := Load(writeConfig(t, "[alerts]\ndesktop = true\n\n[[agent]]\nname = \"aider\"\nnew = [\"aider\"]\n")); err != nil {
		t.Fatalf("alerts next to agents: %v", err)
	}
}
