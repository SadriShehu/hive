package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/sadrishehu/hive/internal/agent"
)

// hookEvents are the Copilot CLI 1.0 events hive listens to. Copilot has no
// notification or permission-prompt hook, so a session can't report "needs you".
var hookEvents = []string{
	"sessionStart", "userPromptSubmitted", "preToolUse", "postToolUse",
	"errorOccurred", "agentStop", "sessionEnd", "subagentStart", "subagentStop",
}

// Install adds Copilot CLI hooks without changing other hooks or settings.
func (a *Adapter) Install(hiveBin string) (string, error) {
	path := a.hooksPath()
	data, original, err := readHooks(path)
	if err != nil {
		return "", err
	}
	out := data
	if !gjson.GetBytes(out, "version").Exists() {
		if out, err = sjson.SetBytes(out, "version", 1); err != nil {
			return "", err
		}
	}
	added, updated := 0, 0
	for _, event := range hookEvents {
		// Copilot runs command hooks through a shell: bash, powershell or command.
		command := agent.ShellQuote(hiveBin) + " hook copilot " + event
		hook, _ := json.Marshal(struct {
			Type    string `json:"type"`
			Bash    string `json:"bash"`
			Timeout int    `json:"timeoutSec"`
		}{"command", command, 10})
		refs := findOurs(out, event)
		if len(refs) == 0 {
			if out, err = sjson.SetRawBytes(out, "hooks."+event+".-1", hook); err != nil {
				return "", err
			}
			added++
			continue
		}
		for _, i := range refs {
			p := fmt.Sprintf("hooks.%s.%d.bash", event, i)
			if gjson.GetBytes(out, p).String() == command {
				continue
			}
			if out, err = sjson.SetBytes(out, p, command); err != nil {
				return "", err
			}
			updated++
		}
	}
	if added == 0 && updated == 0 {
		return "already installed in " + path, nil
	}
	if err := writeHooks(path, original, data, out); err != nil {
		return "", err
	}
	return fmt.Sprintf("added %d and updated %d hooks in %s", added, updated, path), nil
}

// Uninstall removes only hook entries that call hive.
func (a *Adapter) Uninstall() (string, error) {
	path := a.hooksPath()
	data, original, err := readHooks(path)
	if err != nil {
		return "", err
	}
	out, removed := data, 0
	for _, event := range hookEvents {
		for _, i := range slices.Backward(findOurs(out, event)) {
			if out, err = sjson.DeleteBytes(out, fmt.Sprintf("hooks.%s.%d", event, i)); err != nil {
				return "", err
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
	hooks := gjson.GetBytes(out, "hooks")
	if hooks.IsObject() && len(hooks.Map()) == 0 {
		if out, err = sjson.DeleteBytes(out, "hooks"); err != nil {
			return "", err
		}
	}
	if len(gjson.GetBytes(out, "hooks").Map()) == 0 && len(gjson.ParseBytes(out).Map()) == 1 &&
		gjson.GetBytes(out, "version").Exists() {
		_, backupErr := os.Stat(path + ".bak-hive")
		if backupErr == nil {
			if err := writeHooks(path, original, data, out); err != nil {
				return "", err
			}
			return fmt.Sprintf("removed %d hooks from %s", removed, path), nil
		}
		if !errors.Is(backupErr, fs.ErrNotExist) {
			return "", backupErr
		}
		if err := backupHooks(path, data); err != nil {
			return "", err
		}
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return fmt.Sprintf("removed %d hooks and %s", removed, path), nil
	}
	if err := writeHooks(path, original, data, out); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed %d hooks from %s", removed, path), nil
}

func readHooks(path string) (data, original []byte, err error) {
	data, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte(`{"version":1,"hooks":{}}`), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	original = data
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte(`{"version":1,"hooks":{}}`), original, nil
	}
	if !gjson.ValidBytes(data) {
		return nil, nil, fmt.Errorf("%s is not valid JSON; not touching it", path)
	}
	version := gjson.GetBytes(data, "version")
	if version.Exists() && version.Int() != 1 {
		return nil, nil, fmt.Errorf("%s has unsupported hook version %s", path, version.Raw)
	}
	hooks := gjson.GetBytes(data, "hooks")
	if hooks.Exists() && !hooks.IsObject() {
		return nil, nil, fmt.Errorf("%s has a non-object hooks value; not touching it", path)
	}
	for _, event := range hookEvents {
		list := gjson.GetBytes(data, "hooks."+event)
		if list.Exists() && !list.IsArray() {
			return nil, nil, fmt.Errorf("%s has a non-array %s hook list; not touching it", path, event)
		}
	}
	return data, original, nil
}

func writeHooks(path string, original, baseline, edited []byte) error {
	if bytes.Equal(bytes.TrimSpace(baseline), bytes.TrimSpace(edited)) {
		return nil
	}
	if err := backupHooks(path, original); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, edited, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	return agent.WriteFileAtomic(path, buf.Bytes(), 0o600)
}

func backupHooks(path string, original []byte) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.WriteFile(path+".bak-hive", original, 0o600); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}
