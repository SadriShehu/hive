package codex

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

var hookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PostToolUse", "PermissionRequest",
	"Stop", "Interrupt", "SessionEnd", "SubagentStart", "SubagentStop",
}

var ours = regexp.MustCompile(`hive'?\s+hook\s+codex\b`)

const trustNote = " (Codex asks you to trust the new hooks when it next starts)"

type hookRef struct {
	event   string
	entry   int
	hook    int
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

// Installed reports whether hooks.json calls hive on session start.
func (a *Adapter) Installed() bool {
	data, err := os.ReadFile(a.hooksPath())
	return err == nil && len(findOurs(data, "SessionStart")) > 0
}

// Install adds hive's hooks to Codex's hooks.json, keeping everything else.
func (a *Adapter) Install(hiveBin string) (string, error) {
	path := a.hooksPath()
	data, err := readHooks(path)
	if err != nil {
		return "", err
	}
	command := agent.ShellQuote(hiveBin) + " hook codex"
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
	if err := writeHooks(path, data, out); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("added %d hooks to %s", added, path)
	if updated > 0 {
		msg = fmt.Sprintf("added %d and updated %d hooks in %s", added, updated, path)
	}
	return msg + trustNote, nil
}

// Uninstall removes only the hooks that call hive.
func (a *Adapter) Uninstall() (string, error) {
	path := a.hooksPath()
	data, err := readHooks(path)
	if err != nil {
		return "", err
	}
	out := data
	removed := 0
	for _, event := range hookEvents {
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
	if len(gjson.ParseBytes(out).Map()) == 0 && !hasBackup(path) {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return fmt.Sprintf("removed %d hooks and %s", removed, path), nil
	}
	if err := writeHooks(path, data, out); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed %d hooks from %s", removed, path), nil
}

func hasBackup(path string) bool {
	_, err := os.Stat(path + ".bak-hive")
	return err == nil
}

func readHooks(path string) ([]byte, error) {
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
	if hooks := gjson.GetBytes(data, "hooks"); hooks.Exists() && !hooks.IsObject() {
		return nil, fmt.Errorf("%s has a non-object hooks value; not touching it", path)
	}
	return data, nil
}

func writeHooks(path string, original, edited []byte) error {
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
	return agent.WriteFileAtomic(path, buf.Bytes(), 0o600)
}
