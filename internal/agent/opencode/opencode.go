// Package opencode connects opencode to hive through a plugin.
package opencode

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
)

const name = "opencode"

//go:embed hive.js
var pluginSource string

//go:embed hive.v2.js
var pluginSourceV2 string

// marker is the first line of both plugins; hive only touches files that carry it.
var marker = strings.SplitN(pluginSource, "\n", 2)[0]

// Adapter is the opencode adapter. The zero value uses the user's opencode
// files; the fields point it elsewhere.
type Adapter struct {
	ConfigDir string // where install writes the plugin
	DBPath    string // opencode's session database

	// OpenCodeBin is the binary whose version decides which plugin template
	// install writes. Empty means "opencode" on PATH.
	OpenCodeBin string
}

// New returns the adapter for the user's opencode configuration.
func New() *Adapter { return &Adapter{} }

// Spec describes opencode.
func (a *Adapter) Spec() agent.Spec {
	return agent.Spec{
		Name:     name,
		New:      []string{"opencode", "--model", "{model}", "--prompt", "{prompt}"},
		Resume:   []string{"opencode", "--session", "{id}", "--prompt", "{prompt}"},
		Process:  []string{"opencode"},
		Headless: []string{"run"},

		TitleFlags:      []string{"--title"},
		SessionFlags:    []string{"-s", "--session"},
		ResumeSubagents: true,
	}
}

// configDir is the user's opencode config directory.
func configDir() string {
	if d := os.Getenv("OPENCODE_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	return filepath.Join(paths.Home(), ".config", "opencode")
}

func (a *Adapter) pluginPath() string {
	dir := a.ConfigDir
	if dir == "" {
		dir = configDir()
	}
	return filepath.Join(dir, "plugin", "hive.js")
}

func (a *Adapter) dbPath() string {
	if a.DBPath != "" {
		return a.DBPath
	}
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		dir = filepath.Join(paths.Home(), ".local", "share")
	}
	return filepath.Join(dir, "opencode", "opencode.db")
}

func (a *Adapter) openDB(busy time.Duration) (*sql.DB, error) {
	path := a.dbPath()
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", path, busy.Milliseconds()))
}

// ParseHook reads events from the plugin, which speaks the generic contract.
func (a *Adapter) ParseHook(stdin []byte, _ []string, _ func(string) string) ([]agent.Event, error) {
	return agent.ParseJSON(name, stdin)
}

// Installed reports whether hive's plugin is in place.
func (a *Adapter) Installed() bool {
	data, err := os.ReadFile(a.pluginPath())
	return err == nil && strings.HasPrefix(string(data), marker)
}

// versionRe matches the leading version number of `opencode --version`.
var versionRe = regexp.MustCompile(`(\d+)\.`)

// pluginTemplate picks the plugin for the installed opencode: OpenCode 2 and
// newer load a default-export definition with an id and a setup function,
// OpenCode 1 a named function export. ok reports whether the version could
// be determined; on failure install falls back to the OpenCode 1 plugin.
func (a *Adapter) pluginTemplate() (string, int, bool) {
	bin := a.OpenCodeBin
	if bin == "" {
		bin = "opencode"
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return pluginSource, 0, false
	}
	m := versionRe.FindSubmatch(out)
	if m == nil {
		return pluginSource, 0, false
	}
	major, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return pluginSource, 0, false
	}
	if major >= 2 {
		return pluginSourceV2, major, true
	}
	return pluginSource, major, true
}

// Install writes the plugin, pointed at hiveBin.
func (a *Adapter) Install(hiveBin string) (string, error) {
	path := a.pluginPath()
	changes, err := a.PlanInstall(hiveBin)
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "already installed at " + path, nil
	}
	if err := agent.Apply(changes); err != nil {
		return "", err
	}
	if _, major, ok := a.pluginTemplate(); ok {
		return fmt.Sprintf("wrote plugin %s for opencode %d (running sessions pick it up after a restart)", path, major), nil
	}
	return "wrote plugin " + path + " (running sessions pick it up after a restart)", nil
}

// PlanInstall returns the change Install would make, without making it.
func (a *Adapter) PlanInstall(hiveBin string) ([]agent.Change, error) {
	path := a.pluginPath()
	bin, _ := json.Marshal(hiveBin)
	src, _, _ := a.pluginTemplate()
	content := strings.Replace(src, "__HIVE_BIN__", string(bin), 1)
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && string(existing) == content:
		return nil, nil
	case err == nil && !strings.HasPrefix(string(existing), marker):
		return nil, errors.New(path + " exists and was not written by hive; not touching it")
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	return []agent.Change{{Path: path, Old: existing, New: []byte(content), Mode: 0o644}}, nil
}

// Uninstall removes the plugin if hive wrote it.
func (a *Adapter) Uninstall() (string, error) {
	path := a.pluginPath()
	changes, err := a.PlanUninstall()
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "not installed at " + path, nil
	}
	if err := agent.Apply(changes); err != nil {
		return "", err
	}
	return "removed plugin " + path, nil
}

// PlanUninstall returns the change Uninstall would make, without making it.
func (a *Adapter) PlanUninstall() ([]agent.Change, error) {
	path := a.pluginPath()
	if !a.Installed() {
		return nil, nil
	}
	existing, _, err := agent.Current(path)
	if err != nil {
		return nil, err
	}
	return []agent.Change{{Path: path, Old: existing, Remove: true}}, nil
}
