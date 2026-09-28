package tmux

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// bindingMarker precedes the key binding hive adds to tmux.conf.
const bindingMarker = "# hive: agent tree popup (`hive uninstall tmux` removes these two lines)"

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

// popupArgs is the tmux command that opens hive's tree in a popup.
func popupArgs(hiveBin string) []string {
	return []string{"display-popup", "-E", "-w", "90%", "-h", "85%", "-T", " hive ", quote(hiveBin) + " popup"}
}

func quote(s string) string {
	if strings.ContainsAny(s, " '\"\\$`") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

func bindingLine(key, hiveBin string) string {
	return fmt.Sprintf(`bind-key %s display-popup -E -w 90%% -h 85%% -T " hive " "%s popup"`, key, quote(hiveBin))
}

// InstallBinding binds prefix+key to hive's popup in the tmux config and, if
// a server is running, right away. Safe to rerun.
func InstallBinding(conf, key, hiveBin string) (string, error) {
	data, err := os.ReadFile(conf)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	want := bindingMarker + "\n" + bindingLine(key, hiveBin)
	lines := strings.Split(string(data), "\n")
	var out []string
	replaced := false
	for i := 0; i < len(lines); i++ {
		if lines[i] == bindingMarker {
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
	msg := "already bound in " + conf
	if text != string(data) {
		if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(conf, []byte(text), 0o644); err != nil {
			return "", err
		}
		msg = "bound prefix+" + key + " to the hive popup in " + conf
	}
	if Available() {
		if _, err := run(append([]string{"bind-key", key}, popupArgs(hiveBin)...)...); err == nil {
			msg += " (active now)"
		}
	}
	return msg, nil
}

// UninstallBinding removes what InstallBinding added.
func UninstallBinding(conf string) (string, error) {
	data, err := os.ReadFile(conf)
	if errors.Is(err, fs.ErrNotExist) {
		return "not installed", nil
	}
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	var out []string
	var key string
	for i := 0; i < len(lines); i++ {
		if lines[i] == bindingMarker {
			if i+1 < len(lines) {
				if f := strings.Fields(lines[i+1]); len(f) > 1 {
					key = f[1]
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
	if key == "" {
		return "not installed in " + conf, nil
	}
	if err := os.WriteFile(conf, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return "", err
	}
	if Available() {
		run("unbind-key", key)
	}
	return "removed the prefix+" + key + " binding from " + conf, nil
}

// BindingInstalled reports whether conf has hive's binding.
func BindingInstalled(conf string) bool {
	data, err := os.ReadFile(conf)
	return err == nil && strings.Contains(string(data), bindingMarker)
}
