package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/sadrishehu/hive/internal/agent"
)

// hookEvents are the Claude Code hooks hive listens to.
var hookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PostToolUse", "Notification",
	"Stop", "StopFailure", "SessionEnd", "SubagentStart", "SubagentStop",
}

// ours recognizes the hook command hive installs, wherever the binary lives.
var ours = regexp.MustCompile(`hive'?\s+hook\s+claude\b`)

// hookRef locates one of our hook commands inside settings.json.
type hookRef struct {
	event   string
	entry   int // index in hooks.<event>
	hook    int // index in hooks.<event>.<entry>.hooks
	command string
}

func findOurs(data []byte, event string) []hookRef {
	var refs []hookRef
	gjson.GetBytes(data, "hooks."+event).ForEach(func(i, entry gjson.Result) bool {
		entry.Get("hooks").ForEach(func(j, h gjson.Result) bool {
			if cmd := h.Get("command").String(); ours.MatchString(cmd) {
				refs = append(refs, hookRef{event, int(i.Int()), int(j.Int()), cmd})
			}
			return true
		})
		return true
	})
	return refs
}

// Installed reports whether settings.json calls hive on session start.
func (a *Adapter) Installed() bool {
	data, err := os.ReadFile(a.settingsPath())
	return err == nil && len(findOurs(data, "SessionStart")) > 0
}

// Install adds a `hive hook claude` command to each hook event hive uses,
// leaving every other setting and hook as it was.
func (a *Adapter) Install(hiveBin string) (string, error) {
	path := a.settingsPath()
	data, err := readSettings(path)
	if err != nil {
		return "", err
	}
	command := agent.ShellQuote(hiveBin) + " hook claude"
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	entry, _ := json.Marshal(struct {
		Hooks []hook `json:"hooks"`
	}{[]hook{{"command", command, 10}}})
	out := data
	var added, updated int
	for _, event := range hookEvents {
		refs := findOurs(out, event)
		if len(refs) == 0 {
			if out, err = sjson.SetRawBytes(out, "hooks."+event+".-1", entry); err != nil {
				return "", err
			}
			added++
			continue
		}
		for _, r := range refs {
			if r.command == command {
				continue
			}
			// hive moved: point the existing hook at the new binary.
			p := fmt.Sprintf("hooks.%s.%d.hooks.%d.command", r.event, r.entry, r.hook)
			if out, err = sjson.SetBytes(out, p, command); err != nil {
				return "", err
			}
			updated++
		}
	}
	if added == 0 && updated == 0 {
		return "already installed in " + path, nil
	}
	if err := writeSettings(path, data, out); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("added %d hooks to %s", added, path)
	if updated > 0 {
		msg = fmt.Sprintf("added %d and updated %d hooks in %s", added, updated, path)
	}
	return msg + " (new sessions report right away; running ones once Claude reloads its settings)", nil
}

// Uninstall removes hive's hook commands, and any hook entry or event list
// left empty by that.
func (a *Adapter) Uninstall() (string, error) {
	path := a.settingsPath()
	data, err := readSettings(path)
	if err != nil {
		return "", err
	}
	out := data
	removed := 0
	for _, event := range hookEvents {
		// Delete from the end so earlier indexes stay valid.
		for _, r := range slices.Backward(findOurs(out, event)) {
			entryPath := fmt.Sprintf("hooks.%s.%d", r.event, r.entry)
			if out, err = sjson.DeleteBytes(out, fmt.Sprintf("%s.hooks.%d", entryPath, r.hook)); err != nil {
				return "", err
			}
			if len(gjson.GetBytes(out, entryPath+".hooks").Array()) == 0 {
				if out, err = sjson.DeleteBytes(out, entryPath); err != nil {
					return "", err
				}
			}
			removed++
		}
		if list := gjson.GetBytes(out, "hooks."+event); list.Exists() && len(list.Array()) == 0 {
			if out, err = sjson.DeleteBytes(out, "hooks."+event); err != nil {
				return "", err
			}
		}
	}
	if removed == 0 {
		return "not installed in " + path, nil
	}
	if h := gjson.GetBytes(out, "hooks"); h.IsObject() && len(h.Map()) == 0 {
		if out, err = sjson.DeleteBytes(out, "hooks"); err != nil {
			return "", err
		}
	}
	if err := writeSettings(path, data, out); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed %d hooks from %s", removed, path), nil
}

func readSettings(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{}"), nil
	}
	if !gjson.ValidBytes(data) {
		return nil, fmt.Errorf("%s is not valid JSON; not touching it", path)
	}
	return data, nil
}

// writeSettings backs up the original, then writes the edited file with
// Claude Code's own two-space layout. Key order is preserved.
func writeSettings(path string, original, edited []byte) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.WriteFile(path+".bak-hive", original, 0o600); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, edited, "", "  "); err != nil {
		return err
	}
	buf.WriteString("\n")
	return agent.WriteFileAtomic(path, buf.Bytes(), 0o644)
}
