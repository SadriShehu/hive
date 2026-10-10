package usage

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

// Signal is one kind of work a task asks of a model.
type Signal string

const (
	Reasoning Signal = "reasoning"
	Coding    Signal = "coding"
	Debugging Signal = "debugging"
	ToolUse   Signal = "tool_use"
)

// Signals lists every signal, in the order they are shown.
var Signals = []Signal{Reasoning, Coding, Debugging, ToolUse}

// Tiers rank models coarsely. A tier gives a model the same rating on every
// signal and a speed; the per-signal fields of a Model refine it.
const (
	TierFrontier = "frontier"
	TierStrong   = "strong"
	TierBalanced = "balanced"
	TierFast     = "fast"
)

var Tiers = []string{TierFrontier, TierStrong, TierBalanced, TierFast}

type tierRating struct{ capability, speed int }

var tierRatings = map[string]tierRating{
	TierFrontier: {capability: 10, speed: 3},
	TierStrong:   {capability: 8, speed: 5},
	TierBalanced: {capability: 6, speed: 7},
	TierFast:     {capability: 4, speed: 10},
}

// MaxRating is the top of the 0 to 10 scale ratings and needs use.
const MaxRating = 10

type Model struct {
	Name          string  `toml:"name"`
	Input         float64 `toml:"input"`
	Output        float64 `toml:"output"`
	CacheRead     float64 `toml:"cache_read"`
	CacheWrite    float64 `toml:"cache_write"`
	CacheWrite1h  float64 `toml:"cache_write_1h"`
	ContextWindow int64   `toml:"context_window"`

	Tier      string `toml:"tier"`
	Reasoning int    `toml:"reasoning"`
	Coding    int    `toml:"coding"`
	Debugging int    `toml:"debugging"`
	ToolUse   int    `toml:"tool_use"`
	Speed     int    `toml:"speed"`
}

func (m Model) Priced() bool { return m.Input > 0 || m.Output > 0 }

// Rated reports whether the model can be picked for a task: it has a tier
// or at least one rating.
func (m Model) Rated() bool {
	return m.Tier != "" || m.Reasoning > 0 || m.Coding > 0 || m.Debugging > 0 || m.ToolUse > 0
}

// Rating is the model's score on s: the field when set, else its tier's.
func (m Model) Rating(s Signal) int {
	explicit := map[Signal]int{Reasoning: m.Reasoning, Coding: m.Coding, Debugging: m.Debugging, ToolUse: m.ToolUse}[s]
	if explicit > 0 {
		return explicit
	}
	return tierRatings[m.Tier].capability
}

// SpeedRating is how fast the model answers, 1 slow to 10 fast.
func (m Model) SpeedRating() int {
	if m.Speed > 0 {
		return m.Speed
	}
	return tierRatings[m.Tier].speed
}

// Power sums the ratings: how much model a task gets.
func (m Model) Power() int {
	total := 0
	for _, s := range Signals {
		total += m.Rating(s)
	}
	return total
}

// PricePerMillion is what a million tokens in and a million out cost; 0
// when the price isn't known.
func (m Model) PricePerMillion() float64 { return m.Input + m.Output }

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
		case m.Tier != "" && !slices.Contains(Tiers, m.Tier):
			return fmt.Errorf("model %q: tier must be one of %s", m.Name, strings.Join(Tiers, ", "))
		case outOfScale(m.Reasoning, m.Coding, m.Debugging, m.ToolUse, m.Speed):
			return fmt.Errorf("model %q: ratings go from 1 to %d", m.Name, MaxRating)
		}
		seen[Normalize(m.Name)] = true
	}
	return nil
}

func outOfScale(ratings ...int) bool {
	for _, r := range ratings {
		if r < 0 || r > MaxRating {
			return true
		}
	}
	return false
}

var (
	oneMillionSuffix = regexp.MustCompile(`\[1m\]$`)
	dateSuffix       = regexp.MustCompile(`-\d{8}$`)
	dottedVersion    = regexp.MustCompile(`(\d)\.(\d)`)
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

// Match finds the row for a model the way a tool names it: with a provider
// in front (deepseek/deepseek-v4-pro), a version after @ (claude-opus-5-5@default)
// or dots in the version (claude-opus-4.6, gpt-5.5). Unlike Lookup it takes
// no prefix match, so a variant hive doesn't know stays unrated.
func (c Catalog) Match(id string) (Model, bool) {
	id, _, _ = strings.Cut(id, "@")
	for _, candidate := range []string{Normalize(id), Normalize(dottedVersion.ReplaceAllString(id, "$1-$2"))} {
		if m, ok := c.models[candidate]; ok {
			return m, true
		}
		if i := strings.LastIndex(candidate, "/"); i >= 0 {
			if m, ok := c.models[candidate[i+1:]]; ok {
				return m, true
			}
		}
	}
	return Model{}, false
}

// All returns every row, by name.
func (c Catalog) All() []Model {
	out := make([]Model, 0, len(c.models))
	for _, m := range c.models {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Model) int { return cmp.Compare(a.Name, b.Name) })
	return out
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
