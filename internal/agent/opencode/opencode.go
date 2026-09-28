// Package opencode connects opencode to hive through a plugin.
package opencode

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
)

const name = "opencode"

//go:embed hive.js
var pluginSource string

// marker is the first line of the plugin; hive only touches files that carry it.
var marker = strings.SplitN(pluginSource, "\n", 2)[0]

// Adapter is the opencode adapter. The zero value uses the user's opencode
// files; the fields point it elsewhere.
type Adapter struct {
	ConfigDir string // where install writes the plugin
	DBPath    string // opencode's session database
}

// New returns the adapter for the user's opencode configuration.
func New() *Adapter { return &Adapter{} }

// Spec describes opencode.
func (a *Adapter) Spec() agent.Spec {
	return agent.Spec{
		Name:     name,
		New:      []string{"opencode", "--prompt", "{prompt}"},
		Resume:   []string{"opencode", "--session", "{id}", "--prompt", "{prompt}"},
		Process:  []string{"opencode"},
		Headless: []string{"run"},

		TitleFlags:   []string{"--title"},
		SessionFlags: []string{"-s", "--session"},
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

// ParseHook reads events from the plugin, which speaks the generic contract.
func (a *Adapter) ParseHook(stdin []byte, _ []string, _ func(string) string) ([]agent.Event, error) {
	return agent.ParseJSON(name, stdin)
}

// Installed reports whether hive's plugin is in place.
func (a *Adapter) Installed() bool {
	data, err := os.ReadFile(a.pluginPath())
	return err == nil && strings.HasPrefix(string(data), marker)
}

// Install writes the plugin, pointed at hiveBin.
func (a *Adapter) Install(hiveBin string) (string, error) {
	path := a.pluginPath()
	bin, _ := json.Marshal(hiveBin)
	content := strings.Replace(pluginSource, "__HIVE_BIN__", string(bin), 1)
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && string(existing) == content:
		return "already installed at " + path, nil
	case err == nil && !strings.HasPrefix(string(existing), marker):
		return "", errors.New(path + " exists and was not written by hive; not touching it")
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := agent.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "wrote plugin " + path + " (running sessions pick it up after a restart)", nil
}

// Uninstall removes the plugin if hive wrote it.
func (a *Adapter) Uninstall() (string, error) {
	path := a.pluginPath()
	if !a.Installed() {
		return "not installed at " + path, nil
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return "removed plugin " + path, nil
}
