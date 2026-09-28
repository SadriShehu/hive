// Package proc snapshots the process table so hive can walk from an agent up
// to whatever started it.
package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Proc is one process. Args is the command line split on whitespace, as ps
// reports it, so arguments containing spaces arrive in pieces.
type Proc struct {
	PID  int
	PPID int
	Args []string
}

// launchers run a program given as their first argument.
var launchers = []string{"node", "bun", "deno"}

// Argv is the command line with any script launcher removed, so
// "node /usr/local/bin/claude -p" reads as "claude -p".
func (p Proc) Argv() []string {
	if len(p.Args) > 1 && slices.Contains(launchers, base(p.Args[0])) {
		return p.Args[1:]
	}
	return p.Args
}

// Name is the program's base name, e.g. "claude".
func (p Proc) Name() string {
	argv := p.Argv()
	if len(argv) == 0 {
		return ""
	}
	return strings.TrimSuffix(base(argv[0]), ".js")
}

// Runs reports whether p is one of the named programs.
func (p Proc) Runs(names []string) bool {
	return slices.Contains(names, p.Name())
}

func base(arg string) string {
	// Login shells report themselves as "-zsh".
	return strings.TrimPrefix(filepath.Base(arg), "-")
}

// Table maps pid to process.
type Table map[int]Proc

// Snapshot reads the current process table.
func Snapshot() (Table, error) {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return Parse(out), nil
}

// Parse reads the output of `ps -o pid=,ppid=,args=`.
func Parse(out []byte) Table {
	t := Table{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		t[pid] = Proc{PID: pid, PPID: ppid, Args: f[2:]}
	}
	return t
}

// Ancestors returns the processes above pid, nearest first, stopping before
// init/launchd.
func (t Table) Ancestors(pid int) []int {
	var out []int
	p, ok := t[pid]
	for i := 0; ok && p.PPID > 1 && i < 128; i++ {
		out = append(out, p.PPID)
		p, ok = t[p.PPID]
	}
	return out
}

// Cwd returns the working directory of pid, or "" when it can't be read.
func Cwd(pid int) string {
	if runtime.GOOS == "linux" {
		dir, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		return dir
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}
