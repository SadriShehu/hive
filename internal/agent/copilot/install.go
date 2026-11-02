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
	changes, added, updated, err := a.planInstall(hiveBin)
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "already installed in " + path, nil
	}
	if err := agent.Apply(changes); err != nil {
		return "", err
	}
	return fmt.Sprintf("added %d and updated %d hooks in %s", added, updated, path), nil
}

// PlanInstall returns the change Install would make, without making it.
func (a *Adapter) PlanInstall(hiveBin string) ([]agent.Change, error) {
	changes, _, _, err := a.planInstall(hiveBin)
	return changes, err
}

func (a *Adapter) planInstall(hiveBin string) ([]agent.Change, int, int, error) {
	path := a.hooksPath()
	data, _, err := readHooks(path)
	if err != nil {
		return nil, 0, 0, err
	}
	out := data
	if !gjson.GetBytes(out, "version").Exists() {
		if out, err = sjson.SetBytes(out, "version", 1); err != nil {
			return nil, 0, 0, err
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
				return nil, 0, 0, err
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
				return nil, 0, 0, err
			}
			updated++
		}
	}
	if added == 0 && updated == 0 {
		return nil, 0, 0, nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, out, "", "  "); err != nil {
		return nil, 0, 0, err
	}
	buf.WriteByte('\n')
	old, exists, err := agent.Current(path)
	if err != nil {
		return nil, 0, 0, err
	}
	return []agent.Change{{Path: path, Old: old, New: buf.Bytes(), Mode: 0o600, Backup: exists}}, added, updated, nil
}

// Uninstall removes only hook entries that call hive.
func (a *Adapter) Uninstall() (string, error) {
	path := a.hooksPath()
	changes, removed, removedFile, err := a.planUninstall()
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "not installed in " + path, nil
	}
	if err := agent.Apply(changes); err != nil {
		return "", err
	}
	if removedFile {
		return fmt.Sprintf("removed %d hooks and %s", removed, path), nil
	}
	return fmt.Sprintf("removed %d hooks from %s", removed, path), nil
}

// PlanUninstall returns the change Uninstall would make, without making it.
func (a *Adapter) PlanUninstall() ([]agent.Change, error) {
	changes, _, _, err := a.planUninstall()
	return changes, err
}

func (a *Adapter) planUninstall() ([]agent.Change, int, bool, error) {
	path := a.hooksPath()
	data, _, err := readHooks(path)
	if err != nil {
		return nil, 0, false, err
	}
	out, removed := data, 0
	for _, event := range hookEvents {
		for _, i := range slices.Backward(findOurs(out, event)) {
			if out, err = sjson.DeleteBytes(out, fmt.Sprintf("hooks.%s.%d", event, i)); err != nil {
				return nil, 0, false, err
			}
			removed++
		}
		if list := gjson.GetBytes(out, "hooks."+event); list.Exists() && len(list.Array()) == 0 {
			if out, err = sjson.DeleteBytes(out, "hooks."+event); err != nil {
				return nil, 0, false, err
			}
		}
	}
	if removed == 0 {
		return nil, 0, false, nil
	}
	hooks := gjson.GetBytes(out, "hooks")
	if hooks.IsObject() && len(hooks.Map()) == 0 {
		if out, err = sjson.DeleteBytes(out, "hooks"); err != nil {
			return nil, 0, false, err
		}
	}
	old, exists, err := agent.Current(path)
	if err != nil {
		return nil, 0, false, err
	}
	if len(gjson.GetBytes(out, "hooks").Map()) == 0 && len(gjson.ParseBytes(out).Map()) == 1 &&
		gjson.GetBytes(out, "version").Exists() {
		_, backupErr := os.Stat(path + ".bak-hive")
		if errors.Is(backupErr, fs.ErrNotExist) {
			return []agent.Change{{Path: path, Old: old, Remove: true, Backup: exists}}, removed, true, nil
		}
		if backupErr != nil {
			return nil, 0, false, backupErr
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, out, "", "  "); err != nil {
		return nil, 0, false, err
	}
	buf.WriteByte('\n')
	return []agent.Change{{Path: path, Old: old, New: buf.Bytes(), Mode: 0o600, Backup: exists}}, removed, false, nil
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

