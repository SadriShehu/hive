package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// folderTree makes folders to suggest, a hidden one, a file, and links to a
// folder and to a file.
func folderTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"api/v1", "api/v2", "App", "apps", "bin", ".git", "real"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "api.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{"linked": "real", "alink": "api.go"} {
		if err := os.Symlink(filepath.Join(root, to), filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFolderSuggestions(t *testing.T) {
	root := folderTree(t)
	t.Setenv("HOME", root)
	in := func(names ...string) []string {
		for i, n := range names {
			names[i] = root + "/" + n
		}
		return names
	}
	for _, c := range []struct {
		value string
		want  []string
	}{
		{root + "/a", in("api", "App", "apps")},
		{root + "/AP", in("api", "App", "apps")},
		{root + "/", in("api", "App", "apps", "bin", "linked", "real")},
		{root + "/.", in(".git")},
		{root + "/api/", in("api/v1", "api/v2")},
		{"~/b", []string{"~/bin"}},
		{root + "/zzz", nil},
		{root + "/missing/", nil},
	} {
		if got := folderSuggestions(c.value); !slices.Equal(got, c.want) {
			t.Errorf("folderSuggestions(%q) = %q, want %q", c.value, got, c.want)
		}
	}
}

// typeFolder opens the new-agent form and types value into its folder field.
func typeFolder(t *testing.T, m Model, value string) Model {
	t.Helper()
	m = press(t, m, "n")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	return press(t, m, value)
}

func TestNewFormSuggestsFolders(t *testing.T) {
	root := folderTree(t)
	m, ops := setup(t, false)
	m = typeFolder(t, m, root+"/a")
	view := ansi.Strip(m.View())
	for _, want := range []string{"api/", "App/", "apps/", "tab fill in"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}

	// Nothing is picked yet: tab takes the first and lists what's inside it.
	m = press(t, m, "tab")
	if got := m.form.folder.Value(); got != root+"/api/" || m.form.field != 1 || len(m.form.folders) != 2 {
		t.Fatalf("after tab: folder %q, field %d, suggestions %q", got, m.form.field, m.form.folders)
	}
	m = press(t, m, "down", "down", "down", "enter")
	if got := m.form.folder.Value(); got != root+"/api/v2" || m.form.field != 2 || m.form.folders != nil {
		t.Fatalf("after picking: folder %q, field %d, suggestions %q", got, m.form.field, m.form.folders)
	}
	press(t, m, "enter")
	if len(ops.launched) != 1 || ops.launched[0].Cwd != root+"/api/v2" {
		t.Errorf("launched = %+v", ops.launched)
	}
}

func TestFolderListKeepsWhatIsTyped(t *testing.T) {
	root := folderTree(t)
	m, _ := setup(t, false)
	m = typeFolder(t, m, root+"/api")
	if len(m.form.folders) != 1 {
		t.Fatalf("suggestions = %q, want api alone", m.form.folders)
	}
	// ↑ from the first goes back to what's typed, which enter keeps.
	m = press(t, m, "down", "up", "enter")
	if got := m.form.folder.Value(); got != root+"/api" || m.form.field != 2 {
		t.Fatalf("folder %q, field %d; want what was typed and the prompt", got, m.form.field)
	}

	// Coming back lists nothing until the folder changes.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.form.field != 1 || m.form.folders != nil {
		t.Fatalf("back in folder: field %d, suggestions %q", m.form.field, m.form.folders)
	}
	m = press(t, m, "/")
	if len(m.form.folders) != 2 {
		t.Fatalf("suggestions = %q, want v1 and v2", m.form.folders)
	}
	m = press(t, m, "esc")
	if m.mode != modeNew || m.form.folders != nil {
		t.Fatalf("first esc: mode %v, suggestions %q; want the list hidden and the form open", m.mode, m.form.folders)
	}
	m = press(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("second esc left the form open")
	}
}

func TestFolderListScrollsAndFits(t *testing.T) {
	root := t.TempDir()
	for i := range 12 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := setup(t, false)
	m = typeFolder(t, m, root+"/")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "d07/") || strings.Contains(view, "d08/") || !strings.Contains(view, "1–8 of 12") {
		t.Errorf("want d00–d07 and a count:\n%s", view)
	}
	m = press(t, m, "down", "down", "down", "down", "down", "down", "down", "down", "down")
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "› d08/") || strings.Contains(view, "d00/") || !strings.Contains(view, "2–9 of 12") {
		t.Errorf("want the list scrolled to d08:\n%s", view)
	}

	for _, size := range [][2]int{{30, 8}, {80, 12}, {80, 16}, {99, 24}} {
		mm := step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(mm.View(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, ansi.Strip(l))
			}
		}
	}
}
