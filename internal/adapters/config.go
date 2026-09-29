package adapters

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Agent is one [[agent]] table of config.toml: a tool hive doesn't ship with,
// or changes to a built-in one. Fields left out keep the built-in values.
type Agent struct {
	Name         string   `toml:"name"`
	New          []string `toml:"new"`
	Resume       []string `toml:"resume"`
	Process      []string `toml:"process"`
	Headless     []string `toml:"headless"`
	TitleFlags   []string `toml:"title_flags"`
	SessionFlags []string `toml:"session_flags"`
	ParentEnv    string   `toml:"parent_env"`
}

var toolName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load returns the built-in adapters with the config file at path applied.
// A missing file is no config; a file with mistakes changes nothing, and Load
// says what is wrong with it.
func Load(path string) ([]agent.Adapter, error) {
	all := Builtin()
	agents, err := readConfig(path)
	if err != nil {
		return all, err
	}
	for _, c := range agents {
		i := slices.IndexFunc(all, func(a agent.Adapter) bool { return a.Spec().Name == c.Name })
		if i < 0 {
			all = append(all, custom{c.apply(agent.Spec{Name: c.Name})})
			continue
		}
		all[i] = overridden{all[i], c.apply(all[i].Spec())}
	}
	return all, nil
}

func readConfig(path string) ([]Agent, error) {
	var cfg struct {
		Agent []Agent `toml:"agent"`
	}
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, fmt.Errorf("%s: unknown setting %q", path, keys[0].String())
	}
	seen := map[string]bool{}
	for _, c := range cfg.Agent {
		switch {
		case !toolName.MatchString(c.Name):
			return nil, fmt.Errorf("%s: agent name %q must be lowercase letters, digits, - or _", path, c.Name)
		case seen[c.Name]:
			return nil, fmt.Errorf("%s: agent %q is defined twice", path, c.Name)
		}
		seen[c.Name] = true
	}
	return cfg.Agent, nil
}

// apply returns spec with the fields c sets.
func (c Agent) apply(spec agent.Spec) agent.Spec {
	set := func(dst *[]string, src []string) {
		if len(src) > 0 {
			*dst = src
		}
	}
	set(&spec.New, c.New)
	set(&spec.Resume, c.Resume)
	set(&spec.Process, c.Process)
	set(&spec.Headless, c.Headless)
	set(&spec.TitleFlags, c.TitleFlags)
	set(&spec.SessionFlags, c.SessionFlags)
	if c.ParentEnv != "" {
		spec.ParentEnv = c.ParentEnv
	}
	if len(spec.Process) == 0 && len(spec.New) > 0 {
		spec.Process = []string{filepath.Base(spec.New[0])}
	}
	return spec
}

// custom is a tool defined only in config.toml. It reports in the generic
// hook format, through a `hive hook <name>` call its user sets up, so hive
// has nothing to install.
type custom struct{ spec agent.Spec }

func (c custom) Spec() agent.Spec { return c.spec }

func (c custom) ParseHook(stdin []byte, _ []string, _ func(string) string) ([]agent.Event, error) {
	return agent.ParseJSON(c.spec.Name, stdin)
}

func (c custom) Install(string) (string, error) {
	return fmt.Sprintf("nothing to install: have %s run `hive hook %[1]s` (see the README)", c.spec.Name), nil
}

func (c custom) Uninstall() (string, error) { return "nothing to remove", nil }
func (c custom) Installed() bool            { return true }

// overridden is a built-in adapter whose Spec config.toml changes. It passes
// the optional interfaces through, so imports, previews and resume checks
// work as before.
type overridden struct {
	agent.Adapter
	spec agent.Spec
}

func (o overridden) Spec() agent.Spec { return o.spec }

func (o overridden) Import(ctx context.Context, st *store.Store, found func(agent.ShellCommand)) (int, error) {
	if imp, ok := o.Adapter.(agent.Importer); ok {
		return imp.Import(ctx, st, found)
	}
	return 0, nil
}

func (o overridden) Tail(s store.Session, n int) ([]agent.Line, error) {
	if t, ok := o.Adapter.(agent.Tailer); ok {
		return t.Tail(s, n)
	}
	return nil, fmt.Errorf("hive can't read %s transcripts", o.spec.Name)
}

func (o overridden) CheckResume(s store.Session) error {
	if c, ok := o.Adapter.(agent.ResumeChecker); ok {
		return c.CheckResume(s)
	}
	return nil
}
