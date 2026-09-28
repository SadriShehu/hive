package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

type hookFlags struct {
	event, session, parent, title, cwd string
	pid                                int
}

func newHookCmd() *cobra.Command {
	var f hookFlags
	cmd := &cobra.Command{
		Use:   "hook <tool> [args]",
		Short: "Receive a lifecycle event from an agent (called by integrations)",
		Long: "Receive a lifecycle event from an agent. Integrations call this; it prints\n" +
			"nothing and always exits 0 so it can never disturb the agent. Errors go to\n" +
			paths.LogPath() + "; set HIVE_DEBUG=1 to log every decision too.\n\n" +
			"Any tool can report by piping JSON: {\"event\": \"start|prompt|busy|idle|attention|end|update\",\n" +
			"\"session_id\", \"pid\", \"parent_id\", \"title\", \"cwd\"}, or with the flags below.",
		Hidden:             true,
		Args:               cobra.MinimumNArgs(1),
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		Run: func(cmd *cobra.Command, args []string) {
			defer func() {
				if r := recover(); r != nil {
					logf("hook %s: panic: %v", args[0], r)
				}
			}()
			if err := runHook(args[0], args[1:], f); err != nil {
				logf("hook %s: %v", args[0], err)
			}
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.event, "event", "", "event type")
	fl.StringVar(&f.session, "session", "", "the tool's session ID")
	fl.StringVar(&f.parent, "parent", "", "parent session ID (<tool>:<id>)")
	fl.StringVar(&f.title, "title", "", "session title")
	fl.StringVar(&f.cwd, "cwd", "", "session folder")
	fl.IntVar(&f.pid, "pid", 0, "agent process ID")
	return cmd
}

func runHook(tool string, args []string, f hookFlags) error {
	events, err := hookEvents(tool, args, f)
	if err != nil {
		return err
	}
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	tr := tracker.New(st, adapters.All(), &tracker.System{})
	if os.Getenv("HIVE_DEBUG") != "" {
		tr.Logf = logf
	}
	var errs []error
	for _, ev := range events {
		errs = append(errs, tr.Ingest(ev))
	}
	return errors.Join(errs...)
}

func hookEvents(tool string, args []string, f hookFlags) ([]agent.Event, error) {
	if f.session != "" {
		t, ok := agent.ParseEventType(f.event)
		if !ok {
			return nil, fmt.Errorf("unknown event %q", f.event)
		}
		return []agent.Event{{Tool: tool, SessionID: f.session, Type: t, PID: f.pid,
			ParentID: f.parent, Title: f.title, Cwd: f.cwd}}, nil
	}
	stdin := readStdin()
	if a, ok := adapters.Get(tool); ok {
		return a.ParseHook(stdin, args, os.Getenv)
	}
	return agent.ParseJSON(tool, stdin)
}

// readStdin returns piped input, or nothing when stdin is a terminal.
func readStdin() []byte {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 64<<20))
	return data
}

// logf appends one line to hive's log, rotating it past 1 MB.
func logf(format string, args ...any) {
	path := paths.LogPath()
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		os.Rename(path, path+".1")
	}
	if err := os.MkdirAll(paths.DataDir(), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%d] %s\n", time.Now().Format(time.RFC3339), os.Getpid(), fmt.Sprintf(format, args...))
}
