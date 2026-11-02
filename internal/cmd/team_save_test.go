package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/store"
)

func TestSaveTeamRoundTripsThroughTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "# my tools and alerts\n[alerts]\ndesktop = true\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tm := store.Team{ID: "t1", Name: "api", Goal: "add the admin API", Cwd: "/work/api",
		Workspace: store.WorkspaceWorktrees, Status: store.TeamActive, LeadRole: "lead", CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateTeam(tm); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Member{
		{TeamID: tm.ID, Role: "lead", Tool: "claude", Model: "claude-opus-5-5", JoinedAt: 1},
		{TeamID: tm.ID, Role: "backend", Tool: "codex", JoinedAt: 2},
		{TeamID: tm.ID, Role: "frontend", Tool: "opencode", Model: "auto", Prompt: "own the web UI\nand the table", JoinedAt: 3},
	} {
		if err := st.AddMember(m); err != nil {
			t.Fatal(err)
		}
	}
	members, err := st.Members(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveTeam(path, tm, members, "api"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), old) {
		t.Fatalf("the old config changed:\n%s", data)
	}
	if want := "# saved from team api on " + time.Now().Format("2006-01-02"); !strings.Contains(string(data), want) {
		t.Fatalf("config lacks %q:\n%s", want, data)
	}

	cfg, err := adapters.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Teams) != 1 {
		t.Fatalf("templates = %+v, want one", cfg.Teams)
	}
	got := cfg.Teams[0]
	if got.Name != "api" || got.Goal != tm.Goal || got.Lead != "claude:claude-opus-5-5" ||
		got.Cwd != tm.Cwd || !got.Worktrees {
		t.Errorf("template = %+v", got)
	}
	want := []adapters.TeamMember{
		{Role: "backend", Tool: "codex"},
		{Role: "frontend", Tool: "opencode", Model: "auto", Prompt: "own the web UI\nand the table"},
	}
	if len(got.Members) != len(want) {
		t.Fatalf("members = %+v, want %+v", got.Members, want)
	}
	for i := range want {
		if got.Members[i] != want[i] {
			t.Errorf("member %d = %+v, want %+v", i, got.Members[i], want[i])
		}
	}
	if !cfg.Alerts.Desktop {
		t.Error("the old settings no longer load")
	}
}

func TestSaveTeamRefusesADuplicateTemplateAndLeavesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "[[team]]\nname = \"api\"\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	tm := store.Team{ID: "t1", Name: "api", CreatedAt: 1, UpdatedAt: 1}
	if err := saveTeam(path, tm, nil, "api"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("saveTeam = %v, want a duplicate refused", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != old {
		t.Fatalf("the config changed after a refusal:\n%s", data)
	}
}

func TestSaveTeamRefusesABrokenConfigAndLeavesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "[alerts\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	tm := store.Team{ID: "t1", Name: "api", CreatedAt: 1, UpdatedAt: 1}
	if err := saveTeam(path, tm, nil, "api"); err == nil {
		t.Fatal("a broken config was accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != old {
		t.Fatalf("the broken config was changed:\n%s", data)
	}
}
