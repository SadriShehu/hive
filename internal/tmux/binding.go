package tmux

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
)

// The marker lines that precede each key binding hive adds to tmux.conf.
const (
	popupMarker = "# hive: agent tree popup (`hive uninstall tmux` removes these two lines)"
	nextMarker  = "# hive: jump to the agent that needs you (`hive uninstall tmux` removes these two lines)"
)

// Binding is a key hive binds after the prefix, written to tmux.conf on the
// line after its marker, which is how uninstall finds it again.
type Binding struct {
	Key    string
	what   string // what the key does, for messages
	marker string
	argv   []string // the tmux command it runs, to bind it live
	text   string   // the same command, as tmux.conf has it
}

// PopupBinding opens hive's tree in a popup.
func PopupBinding(key, hiveBin string) Binding {
	return Binding{Key: key, what: "the hive popup", marker: popupMarker,
		argv: []string{"display-popup", "-E", "-w", "95%", "-h", "90%", "-T", " hive ", quote(hiveBin) + " popup"},
		text: fmt.Sprintf(`display-popup -E -w 95%% -h 90%% -T " hive " "%s popup"`, quote(hiveBin)),
	}
}

// NextBinding jumps to the agent that has needed you longest, and to the
// next one on each press. tmux fills in the client that pressed the key.
func NextBinding(key, hiveBin string) Binding {
	cmd := quote(hiveBin) + " jump --next --client '#{client_name}'"
	return Binding{Key: key, what: "the agent that needs you", marker: nextMarker,
		argv: []string{"run-shell", "-b", cmd},
		text: `run-shell -b "` + cmd + `"`,
	}
}

func (b Binding) line() string { return "bind-key " + b.Key + " " + b.text }

// ConfPath is the tmux config hive edits: ~/.tmux.conf, unless only the XDG
// location exists.
func ConfPath() string {
	home, _ := os.UserHomeDir()
	classic := filepath.Join(home, ".tmux.conf")
	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	if _, err := os.Stat(classic); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Stat(xdg); err == nil {
			return xdg
		}
	}
	return classic
}

func quote(s string) string {
	if strings.ContainsAny(s, " '\"\\$`") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// ShellQuote quotes a word for a command string tmux runs, as the bindings do.
func ShellQuote(s string) string { return quote(s) }

// InstallBindings writes bindings to the tmux config and, if a server is
// running, binds them right away. Safe to rerun: a binding already there is
// replaced in place.
func InstallBindings(conf string, bindings ...Binding) (string, error) {
	changes, bound, err := PlanInstallBindings(conf, bindings...)
	if err != nil {
		return "", err
	}
	msg := "already bound in " + conf
	if len(changes) > 0 {
		if err := agent.Apply(changes); err != nil {
			return "", err
		}
		msg = "bound " + strings.Join(bound, " and ") + " in " + conf
	}
	if Available() {
		live := true
		for _, b := range bindings {
			if _, err := run(append([]string{"bind-key", b.Key}, b.argv...)...); err != nil {
				live = false
			}
		}
		if live {
			msg += " (active now)"
		}
	}
	return msg, nil
}

// PlanInstallBindings returns the tmux.conf change InstallBindings would
// make, without making it, and what the keys would be bound to.
func PlanInstallBindings(conf string, bindings ...Binding) ([]agent.Change, []string, error) {
	data, err := os.ReadFile(conf)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	text := string(data)
	var bound []string
	for _, b := range bindings {
		text = withBinding(text, b)
		bound = append(bound, "prefix+"+b.Key+" to "+b.what)
	}
	if text == string(data) {
		return nil, bound, nil
	}
	old := data
	if errors.Is(err, fs.ErrNotExist) {
		old = nil
	}
	return []agent.Change{{Path: conf, Old: old, New: []byte(text), Mode: 0o644}}, bound, nil
}

// withBinding returns conf with b in place of the line after b's marker, or
// added at the end.
func withBinding(conf string, b Binding) string {
	want := b.marker + "\n" + b.line()
	lines := strings.Split(conf, "\n")
	var out []string
	replaced := false
	for i := 0; i < len(lines); i++ {
		if lines[i] == b.marker {
			if !replaced {
				out = append(out, strings.Split(want, "\n")...)
				replaced = true
			}
			i++ // skip the old binding line
			continue
		}
		out = append(out, lines[i])
	}
	text := strings.Join(out, "\n")
	if !replaced {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + want + "\n"
	}
	return text
}

// UninstallBindings removes every binding InstallBindings added.
func UninstallBindings(conf string) (string, error) {
	changes, keys, exists, err := PlanUninstallBindings(conf)
	if err != nil {
		return "", err
	}
	if !exists {
		return "not installed", nil
	}
	if len(keys) == 0 {
		return "not installed in " + conf, nil
	}
	if err := agent.Apply(changes); err != nil {
		return "", err
	}
	if Available() {
		for _, k := range keys {
			run("unbind-key", k)
		}
	}
	what := "the prefix+" + strings.Join(keys, " and prefix+") + " binding"
	if len(keys) > 1 {
		what += "s"
	}
	return "removed " + what + " from " + conf, nil
}

// PlanUninstallBindings returns the tmux.conf change UninstallBindings would
// make, without making it, the keys it would unbind, and whether conf exists.
func PlanUninstallBindings(conf string) ([]agent.Change, []string, bool, error) {
	data, err := os.ReadFile(conf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	lines := strings.Split(string(data), "\n")
	var out, keys []string
	for i := 0; i < len(lines); i++ {
		if lines[i] == popupMarker || lines[i] == nextMarker {
			if i+1 < len(lines) {
				if f := strings.Fields(lines[i+1]); len(f) > 1 {
					keys = append(keys, f[1])
				}
			}
			i++
			// Drop the blank line install put before the marker.
			if n := len(out); n > 0 && out[n-1] == "" {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	if len(keys) == 0 {
		return nil, nil, true, nil
	}
	return []agent.Change{{Path: conf, Old: data, New: []byte(strings.Join(out, "\n")), Mode: 0o644}}, keys, true, nil
}

// PopupKey returns the key conf binds to hive's popup, or "".
func PopupKey(conf string) string { return boundKey(conf, popupMarker) }

// NextKey returns the key conf binds to the agent that needs you, or "".
func NextKey(conf string) string { return boundKey(conf, nextMarker) }

func boundKey(conf, marker string) string {
	data, err := os.ReadFile(conf)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		if l == marker && i+1 < len(lines) {
			if f := strings.Fields(lines[i+1]); len(f) > 1 {
				return f[1]
			}
		}
	}
	return ""
}
