package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

// roleOn is the caller's role on a team: --as, else the role it plays there,
// else "". --as is checked: an agent may only name a role it plays, never you.
func (e *teamEnv) roleOn(tm store.Team, as string) (string, error) {
	if as != "" {
		if err := e.checkAs(tm, as); err != nil {
			return "", err
		}
		return as, nil
	}
	for _, r := range e.roles {
		if r.Team.ID == tm.ID {
			return r.Member.Role, nil
		}
	}
	return "", nil
}

// taskFlags are the flags every task command takes.
type taskFlags struct {
	team, as string
}

// open resolves the team and who is acting on its board, as say and post do.
func (f *taskFlags) open() (*teamEnv, store.Team, string, error) {
	e, err := openTeamEnv()
	if err != nil {
		return nil, store.Team{}, "", err
	}
	tm, err := e.team(f.team)
	if err != nil {
		e.st.Close()
		return nil, store.Team{}, "", err
	}
	who, err := e.sender(tm, f.as)
	if err != nil {
		e.st.Close()
		return nil, store.Team{}, "", err
	}
	return e, tm, who, nil
}

func newTeamTaskCmd() *cobra.Command {
	f := &taskFlags{}
	cmd := &cobra.Command{
		Use:   "task",
		Short: "The team's board: numbered tasks, each with an owner and the tasks it waits for",
		Long: "Numbered tasks on the team's board. A task has an owner (a role) and can wait for others\n" +
			"(--after): claim refuses it until they are done. done closes a task with a note on what\n" +
			"changed; block says why it is stuck. hive posts each to the channel, to the lead when an\n" +
			"agent leads, and tells the owner of a task when it is ready. A paused team claims nothing.\n\n" +
			"Inside a member, the team and the role are implied.",
	}
	cmd.PersistentFlags().StringVar(&f.team, "team", "", "the team (default: the caller's)")
	cmd.PersistentFlags().StringVar(&f.as, "as", "", "act as this role: a script's, or one the running agent plays")
	cmd.AddCommand(newTaskAddCmd(f), newTaskClaimCmd(f), newTaskDoneCmd(f), newTaskBlockCmd(f), newTaskLsCmd(f))
	return cmd
}

func newTaskAddCmd(f *taskFlags) *cobra.Command {
	var body, owner, after string
	cmd := &cobra.Command{
		Use:   "add <title...>",
		Short: "Add a task; its owner is told when it is ready",
		Long: "Put a task on the board and print its number. --owner gives it to a role, which hive tells\n" +
			"when the task is ready: at once, or when every task in --after is done. --body - reads\n" +
			"the details from stdin.",
		Example: "  hive team task add \"handlers for /admin/users\" --owner backend --after 1,2",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			waits, err := team.ParseAfter(after)
			if err != nil {
				return err
			}
			if body == "-" {
				in, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				body = string(in)
			}
			e, tm, by, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			task, sent, err := e.tr.AddTask(tm, by, strings.Join(args, " "), body, owner, waits)
			if err != nil {
				return err
			}
			fmt.Printf("#%d\n", task.N)
			printSent(sent)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "the details; - reads them from stdin")
	cmd.Flags().StringVar(&owner, "owner", "", "the role that does it")
	cmd.Flags().StringVar(&after, "after", "", "the tasks it waits for: 1,2")
	return cmd
}

func newTaskClaimCmd(f *taskFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "claim <n>",
		Short: "Take a task to work on",
		Long: "Take a task, and print it. A task that waits for tasks that aren't done, or that is another\n" +
			"member's, is refused unless --force. A paused team claims nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := team.ParseTask(args[0])
			if err != nil {
				return err
			}
			e, tm, by, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			task, err := e.tr.ClaimTask(tm, by, n, force)
			if err != nil {
				return err
			}
			fmt.Printf("#%d %s\n", task.N, task.Title)
			if task.Body != "" {
				fmt.Println(task.Body)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "claim it even if it waits for open tasks or is another member's")
	return cmd
}

func newTaskDoneCmd(f *taskFlags) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "done <n>",
		Short: "Close a task, with a note on what changed",
		Long: "Close a task. hive posts it to the channel (to the lead, when an agent leads) and tells the\n" +
			"owner of each task that was waiting for it and is now ready. It prints those tasks.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := team.ParseTask(args[0])
			if err != nil {
				return err
			}
			e, tm, by, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			task, ready, sent, err := e.tr.DoneTask(tm, by, n, note)
			if task.N == 0 {
				return err
			}
			fmt.Printf("#%d done\n", task.N)
			for _, r := range ready {
				owner := r.Owner
				switch owner {
				case by:
					owner = "yours"
				case "":
					owner = "nobody's yet"
				}
				fmt.Printf("ready: #%d %s (%s)\n", r.N, r.Title, owner)
			}
			printSent(sent)
			return err
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "what changed")
	return cmd
}

func newTaskBlockCmd(f *taskFlags) *cobra.Command {
	var why string
	cmd := &cobra.Command{
		Use:   "block <n>",
		Short: "Mark a task blocked, and say why",
		Long: "Mark a task blocked. hive posts why to the channel, to the lead when an agent leads.\n" +
			"Claiming it again takes it up.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := team.ParseTask(args[0])
			if err != nil {
				return err
			}
			e, tm, by, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			task, sent, err := e.tr.BlockTask(tm, by, n, why)
			if task.N == 0 {
				return err
			}
			fmt.Printf("#%d blocked\n", task.N)
			printSent(sent)
			return err
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "what stops it")
	_ = cmd.MarkFlagRequired("why")
	return cmd
}

func newTaskLsCmd(f *taskFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the board's tasks, with their details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, tm, _, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tasks, err := e.st.Tasks(tm.ID)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(taskRows(tasks))
			}
			printTasks(tasks, true)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the tasks, with whether each is ready")
	return cmd
}

// taskRow is a task as the JSON commands print it.
type taskRow struct {
	store.Task
	Ready bool  `json:"ready"`           // open, and nothing it waits for is left
	Waits []int `json:"waits,omitempty"` // the tasks it waits for that aren't done
}

func taskRows(tasks []store.Task) []taskRow {
	rows := []taskRow{}
	for _, t := range tasks {
		rows = append(rows, taskRow{Task: t, Ready: team.Ready(t, tasks), Waits: team.Waiting(t, tasks)})
	}
	return rows
}

// printTasks prints the board: number, status, owner and title, then what
// done changed, why it is blocked, or what it waits for. With bodies, each
// task's details follow it.
func printTasks(tasks []store.Task, bodies bool) {
	if len(tasks) == 0 {
		fmt.Println("  (no tasks yet; `hive team task add` adds one)")
	}
	for _, t := range tasks {
		extra := t.Note
		if w := team.Waiting(t, tasks); len(w) > 0 && !t.Closed() {
			extra = "waits for " + team.Numbers(w)
		}
		line := fmt.Sprintf("  %-4s %-8s %-10s %s", fmt.Sprintf("#%d", t.N), t.Status, team.Clean(t.Owner), team.Clean(t.Title))
		if extra != "" {
			line += " · " + team.Clean(extra)
		}
		fmt.Println(line)
		if bodies && t.Body != "" {
			fmt.Println("       " + strings.ReplaceAll(team.Clean(t.Body), "\n", "\n       "))
		}
	}
}

// printSent says on stderr what happened to each message hive sent.
func printSent(sent []tracker.Sent) {
	for _, s := range sent {
		fmt.Fprintf(os.Stderr, "%s: %s\n", s.Role, s.How)
	}
}

func newTeamWaitCmd() *cobra.Command {
	var ref, as, from, task string
	var anything bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait until a task closes, a message arrives, or the board changes",
		Long: "Block until something happens on the team's board, and print it: a task (\"#4 done by\n" +
			"backend: …\") or a message (\"[team · from role] …\"). With no flag it waits for any task to\n" +
			"be done or blocked, or a message for the caller. --task waits for that task to be done or\n" +
			"blocked; --from for a message from that role (or you); --any for anything: a task added or\n" +
			"moved, or someone writing in the channel. Flags add up: it returns on the first.\n\n" +
			"The messages it prints are taken from the caller's mail, as `hive team inbox` does, so\n" +
			"they aren't typed into its pane again. A paused team keeps its mail.",
		Example: "  hive team wait --task 4 --timeout 30m",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o := tracker.WaitOptions{From: strings.TrimPrefix(from, "@"), Any: anything, Poll: waitPoll}
			if task != "" {
				n, err := team.ParseTask(task)
				if err != nil {
					return err
				}
				o.Task = n
			}
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(ref)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			role, err := e.roleOn(tm, as)
			if err != nil {
				return err
			}
			lines, err := e.tr.WaitTeam(ctx, tm, role, o)
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("nothing after %s", timeout)
			}
			if err != nil {
				return err
			}
			for _, l := range lines {
				fmt.Println(l)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ref, "team", "", "the team (default: the caller's)")
	f.StringVar(&as, "as", "", "wait as this role: a script's, or one the running agent plays")
	f.StringVar(&task, "task", "", "wait for this task to be done or blocked")
	f.StringVar(&from, "from", "", "wait for a message from this role")
	f.BoolVar(&anything, "any", false, "wait for anything on the board")
	f.DurationVar(&timeout, "timeout", 0, "give up after this long (0: as long as it takes)")
	return cmd
}
