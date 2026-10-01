package usage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

type Model struct {
	Name          string  `toml:"name"`
	Input         float64 `toml:"input"`
	Output        float64 `toml:"output"`
	CacheRead     float64 `toml:"cache_read"`
	CacheWrite    float64 `toml:"cache_write"`
	CacheWrite1h  float64 `toml:"cache_write_1h"`
	ContextWindow int64   `toml:"context_window"`
}

func (m Model) Priced() bool { return m.Input > 0 || m.Output > 0 }

func (m Model) Cost(t store.Tokens) float64 {
	write1h := m.CacheWrite1h
	if write1h == 0 {
		write1h = m.CacheWrite
	}
	return (m.Input*float64(t.Input) + m.Output*float64(t.Output) + m.CacheRead*float64(t.CacheRead) +
		m.CacheWrite*float64(t.CacheWrite-t.CacheWrite1h) + write1h*float64(t.CacheWrite1h)) / 1e6
}

type Catalog struct {
	models     map[string]Model
	configured map[string]bool
}

func NewCatalog(builtin Catalog, overrides []Model) Catalog {
	c := Catalog{models: map[string]Model{}, configured: map[string]bool{}}
	for name, m := range builtin.models {
		c.models[name] = m
	}
	for _, m := range overrides {
		name := Normalize(m.Name)
		m.Name = name
		c.models[name] = m
		c.configured[name] = true
	}
	return c
}

func Validate(models []Model) error {
	seen := map[string]bool{}
	for _, m := range models {
		switch {
		case strings.TrimSpace(m.Name) == "":
			return errors.New("a [[model]] needs a name")
		case seen[Normalize(m.Name)]:
			return fmt.Errorf("model %q is defined twice", m.Name)
		case m.Input < 0 || m.Output < 0 || m.CacheRead < 0 || m.CacheWrite < 0 || m.CacheWrite1h < 0 || m.ContextWindow < 0:
			return fmt.Errorf("model %q has a negative value", m.Name)
		}
		seen[Normalize(m.Name)] = true
	}
	return nil
}

var (
	oneMillionSuffix = regexp.MustCompile(`\[1m\]$`)
	dateSuffix       = regexp.MustCompile(`-\d{8}$`)
)

func Normalize(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = oneMillionSuffix.ReplaceAllString(id, "")
	return dateSuffix.ReplaceAllString(id, "")
}

func (c Catalog) Lookup(model string) (Model, bool) {
	id := Normalize(model)
	if m, ok := c.models[id]; ok {
		return m, true
	}
	if i := strings.LastIndex(id, "/"); i >= 0 {
		if m, ok := c.models[id[i+1:]]; ok {
			return m, true
		}
	}
	var best Model
	for name, m := range c.models {
		if strings.HasPrefix(id, name+"-") && len(name) > len(best.Name) {
			best = m
		}
	}
	return best, best.Name != ""
}

func (c Catalog) Apply(u store.Usage) store.Usage {
	if u.CostSource != store.CostTool {
		if cost, ok := c.cost(u.Models); ok {
			u.CostUSD, u.CostSource = cost, store.CostConfig
		}
	}
	if m, ok := c.Lookup(u.Model); ok && m.ContextWindow > 0 && (u.ContextWindow == 0 || c.configured[m.Name]) {
		u.ContextWindow = m.ContextWindow
	}
	return u
}

func (c Catalog) cost(models map[string]store.Tokens) (float64, bool) {
	if len(models) == 0 {
		return 0, false
	}
	total := 0.0
	for name, tokens := range models {
		m, ok := c.Lookup(name)
		if !ok || !m.Priced() {
			return 0, false
		}
		total += m.Cost(tokens)
	}
	return total, true
}
