package tracker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/fusion"
)

// PickOptions says what to pick a model for.
type PickOptions struct {
	Tool     string      // the tool asked for; "" or fusion.Auto lets the mode decide
	Model    string      // fusion.Auto asks for a pick; anything else is kept as given
	Mode     fusion.Mode // what auto means; "" takes the configured mode
	Prompt   string      // the task, read for what it needs
	ParentID string      // the session the new agent runs under, whose tool ties go to
}

// Wants reports whether the options ask hive to pick anything.
func (o PickOptions) Wants() bool {
	return o.Tool == "" || o.Tool == fusion.Auto || o.Model == fusion.Auto
}

// InstalledTools lists the tools hive can start here: those with a start
// command whose program is on PATH.
func (t *Tracker) InstalledTools() []string {
	var out []string
	for _, a := range t.Adapters {
		if spec := a.Spec(); len(spec.New) > 0 {
			if _, err := exec.LookPath(spec.New[0]); err == nil {
				out = append(out, spec.Name)
			}
		}
	}
	return out
}

// ToolModels lists the models tool can run, as it names them: the
// adapter's list, then what the tool itself lists right now, and, for a
// tool that can't list, what its past sessions used.
func (t *Tracker) ToolModels(ctx context.Context, tool string) ([]string, error) {
	a := t.adapter(tool)
	if a == nil {
		return nil, fmt.Errorf("hive doesn't know %q", tool)
	}
	models := slices.Clone(a.Spec().Models)
	listed := false
	if lister, ok := a.(agent.ModelLister); ok {
		live, err := lister.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		listed = live != nil
		models = append(models, live...)
	}
	if !listed {
		seen, err := t.Store.ModelsSeen()
		if err != nil {
			return nil, err
		}
		models = append(models, seen[tool]...)
	}
	return dedupe(models), nil
}

func dedupe(list []string) []string {
	var out []string
	for _, s := range list {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// Candidates pairs each tool with the rated models it can run. A model a
// tool offers under two names (two providers in opencode) counts once.
func (t *Tracker) Candidates(ctx context.Context, tools []string) ([]fusion.Candidate, error) {
	var out []fusion.Candidate
	var errs []error
	for _, tool := range tools {
		if !t.spec(tool).TakesModel() {
			continue
		}
		ids, err := t.ToolModels(ctx, tool)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var names []string
		for _, id := range ids {
			m, ok := t.Models.Match(id)
			if !ok || !m.Rated() || slices.Contains(names, m.Name) {
				continue
			}
			names = append(names, m.Name)
			out = append(out, fusion.Candidate{Tool: tool, ID: id, Model: m})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Pick chooses the tool and model for o. A named tool with model auto gets
// a model of its own; tool auto (or none) picks by the mode: inside the
// configured tool, else the parent's, else the first installed one, or
// across every installed tool that takes a model.
func (t *Tracker) Pick(ctx context.Context, o PickOptions) (fusion.Choice, error) {
	mode, tools, prefer, err := t.scope(o)
	if err != nil {
		return fusion.Choice{}, err
	}
	candidates, err := t.Candidates(ctx, tools)
	if err != nil {
		return fusion.Choice{}, err
	}
	if len(candidates) == 0 {
		if mode == fusion.ModeModel {
			return fusion.Choice{}, fmt.Errorf("no rated model for %s; `hive models %[1]s` lists what it runs, and a [[model]] with a tier rates one", tools[0])
		}
		return fusion.Choice{}, errors.New("no installed tool has a rated model; `hive models` lists them")
	}
	return fusion.Pick(mode, prefer, candidates, fusion.Assess(o.Prompt))
}

// scope resolves o to a mode, the tools whose models compete, and the tool
// that wins ties.
func (t *Tracker) scope(o PickOptions) (fusion.Mode, []string, string, error) {
	named := o.Tool != "" && o.Tool != fusion.Auto
	mode := o.Mode
	if mode == "" {
		mode = t.Fusion.Mode
	}
	if named {
		mode = fusion.ModeModel
	}
	if mode == "" {
		mode = fusion.ModeProvider
	}
	parentTool := t.parentTool(o.ParentID)
	if mode == fusion.ModeProvider {
		tools := t.InstalledTools()
		if len(tools) == 0 {
			return mode, nil, "", errors.New("no agent found on PATH")
		}
		prefer := t.Fusion.Tool
		if prefer == "" {
			prefer = parentTool
		}
		return mode, tools, prefer, nil
	}
	tool := o.Tool
	if !named {
		tool = t.Fusion.Tool
		if tool == "" {
			tool = parentTool
		}
		if tool == "" {
			if installed := t.InstalledTools(); len(installed) > 0 {
				tool = installed[0]
			}
		}
	}
	switch spec := t.spec(tool); {
	case tool == "":
		return mode, nil, "", errors.New("no agent found on PATH")
	case len(spec.New) == 0:
		return mode, nil, "", fmt.Errorf("hive doesn't know how to start %q", tool)
	case !spec.TakesModel():
		return mode, nil, "", fmt.Errorf("hive can't pick a model for %s: its start command has no {model}; add one in config.toml", tool)
	}
	return mode, []string{tool}, tool, nil
}

func (t *Tracker) parentTool(id string) string {
	if id == "" {
		return ""
	}
	s, ok, err := t.Store.Get(id)
	if err != nil || !ok {
		return ""
	}
	return s.Tool
}
