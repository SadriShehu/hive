//go:build unix

package tmux

import (
	"os"
	"os/exec"
	"syscall"
)

func execTmux(args ...string) error {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	if Socket != "" {
		args = append([]string{"-L", Socket}, args...)
	}
	return syscall.Exec(path, append([]string{"tmux"}, args...), os.Environ())
}
