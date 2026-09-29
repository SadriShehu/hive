// Package adapters lists the tools hive knows.
package adapters

import (
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/agent/claude"
	"github.com/sadrishehu/hive/internal/agent/codex"
	"github.com/sadrishehu/hive/internal/agent/copilot"
	"github.com/sadrishehu/hive/internal/agent/opencode"
)

// All returns every known adapter.
func All() []agent.Adapter {
	return []agent.Adapter{claude.New(), opencode.New(), copilot.New(), codex.New()}
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
