package tracker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"unicode"

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
	if models, ok := t.toolModels[tool]; ok {
		return models, nil
	}
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
	models = dedupe(models)
	if t.toolModels != nil {
		t.toolModels[tool] = models
	}
	return models, nil
}

// modelDefault is what a person types for the tool's own default model.
const modelDefault = "default"

// longContextSuffix marks a model's long-context variant ("claude-sonnet-5-5[1m]").
const longContextSuffix = "[1m]"

// ResolveModel turns the model a person asked tool for into the ID its
// model flag takes: "" or "default" is the tool's default (""), auto stays
// auto, an alias the tool takes stays as it is, and a name for an ID
// ("opus 5.5", "Opus-5.5") becomes that ID, keeping a [1m] long-context
// suffix. A model hive can't place, or one that could be several, is an
// error that lists what the tool runs. A tool hive knows no models for takes
// the model as it is: hive can't check it.
func (t *Tracker) ResolveModel(ctx context.Context, tool, model string) (string, error) {
	model = strings.TrimSpace(model)
	switch {
	case model == "" || strings.EqualFold(model, modelDefault):
		return "", nil
	case model == fusion.Auto, tool == "" || tool == fusion.Auto:
		return model, nil
	}
	a := t.adapter(tool)
	if a == nil {
		return "", fmt.Errorf("hive doesn't know %q", tool)
	}
	spec := a.Spec()
	if !spec.TakesModel() {
		return "", fmt.Errorf("hive can't set the model for %s: its start command has no {model}; add one in config.toml", tool)
	}
	if slices.Contains(spec.ModelAliases, model) {
		return model, nil
	}
	base := strings.TrimSuffix(model, longContextSuffix)
	long := model[len(base):]
	ids, err := t.ToolModels(ctx, tool)
	if err != nil {
		return "", fmt.Errorf("can't check model %q for %s: %w", model, tool, err)
	}
	if len(ids) == 0 {
		return model, nil // no list to check it against: the tool decides
	}
	found := matchModel(base, ids, spec.Models)
	switch {
	case len(found) == 1:
		return found[0] + long, nil
	case len(found) > 1:
		return "", fmt.Errorf("model %q could be any of %s's %s; name one", model, tool, strings.Join(found, ", "))
	}
	// The tool's list is what it names now; the price catalog also knows IDs
	// older versions of the tool still take (Claude Code keeps old IDs), but
	// only ones from the tool's own provider.
	if id, ok := t.catalogModel(base, ids); ok {
		return id + long, nil
	}
	return "", fmt.Errorf("%s has no model %q; it runs %s; add one to its [[agent]] models in config.toml if the tool takes it",
		tool, model, listModels(tool, ids))
}

// catalogModel finds a model the price catalog knows, but only when it
// belongs to the same provider the tool's own models come from: claude-*
// for claude, gpt-* for codex, and a provider/id for a tool that lists them
// (opencode's deepseek/…). So claude can't take gpt-5-5.
func (t *Tracker) catalogModel(base string, ids []string) (string, bool) {
	m, ok := t.Models.Match(base)
	if !ok || !sameProvider(base, m.Name, ids) {
		return "", false
	}
	// Keep the provider the person named, as opencode spells its models.
	if i := strings.IndexByte(base, '/'); i >= 0 {
		return base[:i+1] + m.Name, true
	}
	return m.Name, true
}

// provider is the part of a model name that says whose it is: before the
// slash in a provider/id, else the first word of a family name.
func provider(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return ""
}

// sameProvider reports whether a catalog model named name belongs to the
// tool, given the models the tool lists. A provider/id input is checked by
// its provider; a plain family name by the catalog's spelling.
func sameProvider(input, name string, ids []string) bool {
	want := provider(input)
	if !strings.Contains(input, "/") {
		want = provider(name)
	}
	if want == "" {
		return false
	}
	return slices.ContainsFunc(ids, func(id string) bool { return provider(id) == want })
}

// listModels names the models a tool runs, the first dozen of a long list.
func listModels(tool string, ids []string) string {
	const most = 12
	if len(ids) <= most {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more (`hive models %s` lists them)", strings.Join(ids[:most], ", "), len(ids)-most, tool)
}

// matchModel finds the IDs a name stands for, best match first: the ID
// itself, its words in any case and spelling (claude opus 5.5), the words
// it ends with (opus 5.5), then words anywhere in it (opus). The first kind
// that finds any wins; when it finds several, the one the tool lists as
// current wins, or all of them come back.
func matchModel(name string, ids, current []string) []string {
	if slices.Contains(ids, name) {
		return []string{name}
	}
	want := modelWords(name)
	if want == "" {
		return nil
	}
	kinds := []func(have string) bool{
		func(have string) bool { return have == want },
		func(have string) bool { return strings.HasSuffix(" "+have, " "+want) },
		func(have string) bool { return strings.Contains(" "+have+" ", " "+want+" ") },
	}
	for _, matches := range kinds {
		var found, now []string
		for _, id := range ids {
			if matches(modelWords(id)) {
				found = append(found, id)
				if slices.Contains(current, id) {
					now = append(now, id)
				}
			}
		}
		if len(found) > 1 && len(now) == 1 {
			return now
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

// modelWords spells a model's name as lower-case words, its version a word
// per number: "anthropic/claude-opus-4.6@default" is "anthropic claude opus 4 6".
func modelWords(s string) string {
	s, _, _ = strings.Cut(strings.ToLower(s), "@")
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
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
