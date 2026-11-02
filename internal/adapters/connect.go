package adapters

import (
	"os/exec"

	"github.com/sadrishehu/hive/internal/tmux"
)

// NotConnected lists what hive found but isn't hooked into yet: tools on PATH
// whose hooks aren't installed, then tmux when its popup key isn't bound.
func NotConnected() []string {
	var missing []string
	for _, a := range All() {
		spec := a.Spec()
		if len(spec.New) == 0 {
			continue
		}
		if _, err := exec.LookPath(spec.New[0]); err == nil && !a.Installed() {
			missing = append(missing, spec.Name)
		}
	}
	if tmux.Available() && tmux.PopupKey(tmux.ConfPath()) == "" {
		missing = append(missing, "tmux (prefix+a)")
	}
	return missing
}
