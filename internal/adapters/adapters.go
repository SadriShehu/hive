// Package adapters lists the tools hive knows: the built-in ones, and any that
// config.toml adds or changes.
package adapters

import (
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/agent/claude"
	"github.com/sadrishehu/hive/internal/agent/codex"
	"github.com/sadrishehu/hive/internal/agent/copilot"
	"github.com/sadrishehu/hive/internal/agent/opencode"
	"github.com/sadrishehu/hive/internal/paths"
)

// Builtin returns the adapters hive ships with, untouched by config.
func Builtin() []agent.Adapter {
	return []agent.Adapter{claude.New(), opencode.New(), copilot.New(), codex.New()}
}

// All returns every known adapter. A config file with mistakes is ignored
// here, so hooks keep working; `hive doctor` says what is wrong with it.
func All() []agent.Adapter {
	all, _ := Load(paths.ConfigPath())
	return all
}

// Get returns the adapter named name.
func Get(name string) (agent.Adapter, bool) {
	for _, a := range All() {
		if a.Spec().Name == name {
			return a, true
		}
	}
	return nil, false
}
