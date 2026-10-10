// Package fusion picks the model for a task, and in provider mode the tool
// too, from what the task needs: it reads the prompt for the kinds of work
// it asks for and how hard it is, then takes the lightest model that meets
// that bar, so an easy task doesn't spend a frontier model and a hard one
// doesn't get a fast one.
package fusion

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/usage"
)

// Mode says how far a pick may reach.
type Mode string

const (
	// ModeModel changes only the model, inside one tool.
	ModeModel Mode = "model"
	// ModeProvider changes the tool too: any installed tool's models compete.
	ModeProvider Mode = "provider"
)

// Modes lists the modes, in the order they are shown.
var Modes = []Mode{ModeProvider, ModeModel}

// ParseMode reads a mode from a flag or config value.
func ParseMode(s string) (Mode, bool) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case ModeModel, ModeProvider:
		return m, true
	}
	return "", false
}

// Settings is the [fusion] table of config.toml.
type Settings struct {
	Mode Mode   // what `auto` means; ModeProvider when unset
	Tool string // the tool ModeModel picks inside when the command names none
}

// Auto is the tool or model name that asks hive to pick.
const Auto = "auto"

// Need is what a task asks of a model: a level from 0 (not needed) to 10 per
// signal, and the cues that set them.
type Need struct {
	Levels map[usage.Signal]int
	Cues   []string
}

// Levels of difficulty, on the ratings' scale.
const (
	levelLight    = 4
	levelRoutine  = 6
	levelHard     = 8
	levelVeryHard = 10
)

// Assess reads a prompt for the kinds of work it asks for and how hard it
// is. Without a prompt the session is open-ended, so it asks for a strong
// model on everything.
func Assess(prompt string) Need {
	text := words(prompt)
	if strings.TrimSpace(text) == "" {
		return Need{
			Levels: map[usage.Signal]int{usage.Reasoning: levelHard, usage.Coding: levelHard, usage.Debugging: levelHard, usage.ToolUse: levelHard},
			Cues:   []string{"no prompt: an open session takes anything"},
		}
	}
	need := Need{Levels: map[usage.Signal]int{}}
	level, cues := difficulty(text, prompt)
	need.Cues = cues
	for _, s := range usage.Signals {
		if hits := found(text, signalCues[s]); len(hits) > 0 {
			need.Levels[s] = level
			need.Cues = append(need.Cues, fmt.Sprintf("%s: %s", s, strings.Join(hits, ", ")))
		}
	}
	if len(need.Levels) == 0 {
		need.Levels[usage.Coding] = level
		need.Cues = append(need.Cues, "coding: the default kind of work")
	}
	return need
}

// signalCues are the words that mark each kind of work.
var signalCues = map[usage.Signal][]string{
	usage.Debugging: {"debug", "bug", "broken", "fail", "fails", "failing", "failure", "error", "errors", "crash", "crashes",
		"panic", "flaky", "regression", "doesn't work", "does not work", "not working", "stack trace", "traceback",
		"exception", "why does", "why is", "investigate", "root cause", "reproduce", "diagnose", "fix"},
	usage.Reasoning: {"design", "architect", "architecture", "plan", "trade-off", "tradeoff", "trade-offs", "tradeoffs",
		"compare", "evaluate", "decide", "prove", "explain why", "analyze", "analyse", "strategy", "propose", "review",
		"audit", "security", "concurrency", "race", "deadlock", "performance", "optimize", "optimise", "algorithm",
		"complexity", "migrate", "migration", "spec", "rfc", "think through", "reason"},
	usage.Coding: {"implement", "add", "write", "create", "build", "feature", "endpoint", "function", "class", "method",
		"write tests", "add tests", "add a test", "write a test", "unit test", "unit tests", "test coverage", "tests for",
		"refactor", "rename", "port", "convert", "generate", "scaffold", "boilerplate", "crud", "component", "page",
		"form", "schema", "parser", "handler", "cli", "flag", "option", "wire", "hook up", "support"},
	usage.ToolUse: {"run", "execute", "install", "deploy", "release", "publish", "commit", "push", "open a pr",
		"pull request", "rebase", "merge", "grep", "search", "lint", "format", "benchmark", "docker", "kubectl",
		"terraform", "ci", "pipeline", "script", "shell", "bash", "build the", "upgrade", "update the dependencies",
		"bump", "check"},
}

var hardCues = []string{"architecture", "design", "across", "entire", "whole", "every", "all the", "all of", "migrate",
	"migration", "concurrency", "race", "deadlock", "security", "performance", "prove", "carefully", "thorough",
	"thoroughly", "production", "critical", "complex", "complicated", "large", "big", "system-wide", "end to end",
	"end-to-end", "from scratch", "rewrite", "redesign", "distributed", "hard", "difficult", "subtle", "tricky"}

var lightCues = []string{"quick", "quickly", "simple", "small", "typo", "typos", "rename", "one-liner", "one liner",
	"just", "minor", "tiny", "trivial", "comment", "comments", "format", "formatting", "lint", "bump", "readme",
	"docs", "docstring", "whitespace", "indent", "log line", "print"}

var stepMarkers = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)])\s+`)

// difficulty rates how hard the prompt is, from its cues, length and steps.
func difficulty(text, raw string) (int, []string) {
	var cues []string
	level := levelRoutine
	hard := found(text, hardCues)
	light := found(text, lightCues)
	words := len(strings.Fields(raw))
	steps := len(stepMarkers.FindAllString(raw, -1)) + strings.Count(text, " then ")
	switch {
	case len(hard) > 0:
		level = levelHard
		cues = append(cues, "hard: "+strings.Join(hard, ", "))
	case len(light) > 0 && words < 40:
		level = levelLight
		cues = append(cues, "light: "+strings.Join(light, ", "))
	}
	if words > 150 || steps >= 3 {
		level += 2
		cues = append(cues, fmt.Sprintf("long: %d words, %d steps", words, steps))
	} else if words > 60 {
		level++
		cues = append(cues, fmt.Sprintf("long: %d words", words))
	}
	return min(levelVeryHard, max(levelLight, level)), cues
}

// found returns the cues that occur in text, which words() prepared.
func found(text string, cues []string) []string {
	var out []string
	for _, cue := range cues {
		if strings.Contains(text, " "+cue+" ") {
			out = append(out, cue)
		}
	}
	return out
}

var wordPattern = regexp.MustCompile(`[a-z0-9][a-z0-9'-]*`)

// words lowers the prompt to its words with a space around each, so a cue
// of one or more words is found with its spaces and punctuation can't hide
// it.
func words(prompt string) string {
	return " " + strings.Join(wordPattern.FindAllString(strings.ToLower(prompt), -1), " ") + " "
}

// Level is how hard the task is overall: its highest level.
func (n Need) Level() int {
	level := 0
	for _, l := range n.Levels {
		level = max(level, l)
	}
	return level
}

// Signals lists the kinds of work the task asks for, strongest first and
// then in the usual order.
func (n Need) Signals() []usage.Signal {
	var out []usage.Signal
	for _, s := range usage.Signals {
		if n.Levels[s] > 0 {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b usage.Signal) int { return cmp.Compare(n.Levels[b], n.Levels[a]) })
	return out
}

// String names the need: "debugging 8, coding 8".
func (n Need) String() string {
	var parts []string
	for _, s := range n.Signals() {
		parts = append(parts, fmt.Sprintf("%s %d", s, n.Levels[s]))
	}
	return strings.Join(parts, ", ")
}

// Candidate is one model a tool can run.
type Candidate struct {
	Tool  string
	ID    string // the model as the tool names it, for its --model flag
	Model usage.Model
}

// String names the candidate: "claude · claude-opus-5-5".
func (c Candidate) String() string { return c.Tool + " · " + c.ID }

// Choice is the model picked for a task, and why.
type Choice struct {
	Candidate
	Mode      Mode
	Need      Need
	Met       bool // it meets every level the task asks for
	Qualified int  // how many candidates met them
	Reason    string
}

// Pick chooses among candidates for need: the lightest model that meets
// every level it asks for; among equals the cheaper, then the faster, then
// one from prefer, then the first. When nothing meets the bar, the closest
// to it wins. Candidates come in the order their tools list them.
func Pick(mode Mode, prefer string, candidates []Candidate, need Need) (Choice, error) {
	if len(candidates) == 0 {
		return Choice{}, fmt.Errorf("no rated model to pick from; `hive models` lists them")
	}
	signals := need.Signals()
	shortfall := func(c Candidate) int {
		total := 0
		for _, s := range signals {
			total += max(0, need.Levels[s]-c.Model.Rating(s))
		}
		return total
	}
	preferred := func(c Candidate) int {
		if c.Tool == prefer {
			return 0
		}
		return 1
	}
	var qualified []Candidate
	for _, c := range candidates {
		if shortfall(c) == 0 {
			qualified = append(qualified, c)
		}
	}
	choice := Choice{Mode: mode, Need: need, Met: len(qualified) > 0, Qualified: len(qualified)}
	if choice.Met {
		choice.Candidate = slices.MinFunc(qualified, func(a, b Candidate) int {
			return cmp.Or(
				cmp.Compare(a.Model.Power(), b.Model.Power()),
				comparePrice(a.Model, b.Model),
				cmp.Compare(b.Model.SpeedRating(), a.Model.SpeedRating()),
				cmp.Compare(preferred(a), preferred(b)),
			)
		})
		choice.Reason = fmt.Sprintf("%s: the lightest of %d that meet %s", choice.Candidate, len(qualified), need)
		if len(qualified) == 1 {
			choice.Reason = fmt.Sprintf("%s: the only one that meets %s", choice.Candidate, need)
		}
		return choice, nil
	}
	choice.Candidate = slices.MinFunc(candidates, func(a, b Candidate) int {
		return cmp.Or(
			cmp.Compare(shortfall(a), shortfall(b)),
			cmp.Compare(b.Model.Power(), a.Model.Power()),
			cmp.Compare(preferred(a), preferred(b)),
		)
	})
	choice.Reason = fmt.Sprintf("%s: nothing available meets %s; this comes closest", choice.Candidate, need)
	return choice, nil
}

// comparePrice orders priced models by what a million tokens in and out
// cost; a model without a price neither wins nor loses on it.
func comparePrice(a, b usage.Model) int {
	if !a.Priced() || !b.Priced() {
		return 0
	}
	return cmp.Compare(a.PricePerMillion(), b.PricePerMillion())
}
