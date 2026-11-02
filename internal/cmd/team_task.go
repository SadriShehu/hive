package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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
	cmd.AddCommand(newTaskAddCmd(f), newTaskClaimCmd(f), newTaskDoneCmd(f), newTaskBlockCmd(f), newTaskEditCmd(f), newTaskReopenCmd(f), newTaskLsCmd(f))
	return cmd
}

func newTaskAddCmd(f *taskFlags) *cobra.Command {
	var body, owner, after, file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add [title...]",
		Short: "Add a task, or a whole plan with --file; owners are told when ready",
		Long: "Put a task on the board and print its number. --owner gives it to a role, which hive tells\n" +
			"when the task is ready: at once, or when every task in --after is done. --body - reads\n" +
			"the details from stdin.\n\n" +
			"--file adds a whole plan in one call, from a TOML file of [[task]] tables (- reads\n" +
			"stdin): ref, title, owner, body and after. A task waits for earlier refs in the file or\n" +
			"numbers already on the board. hive checks the whole plan before adding any of it, and\n" +
			"tells owners only once every task is in. --dry-run with --file checks it and prints the\n" +
			"tasks it would add, with the numbers they would get.",
		Example: "  hive team task add \"handlers for /admin/users\" --owner backend --after 1,2\n" +
			"  hive team task add --file plan.toml --dry-run",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case file != "" && len(args) > 0:
				return errors.New("give a title or --file, not both")
			case file != "":
				return nil
			case len(args) == 0:
				return errors.New("hive team task add needs a title: hive team task add <title...>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				if body != "" || owner != "" || after != "" {
					return errors.New("--body, --owner and --after go on each task in --file")
				}
				e, tm, by, err := f.open()
				if err != nil {
					return err
				}
				defer e.st.Close()
				return addTaskPlan(e, tm, by, file, dryRun, cmd.InOrStdin())
			}
			if dryRun {
				return errors.New("--dry-run is for --file")
			}
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
			tasks, err := e.st.Tasks(tm.ID)
			if err != nil {
				return err
			}
			fmt.Println(addSaid(task, tasks, sent))
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "the details; - reads them from stdin")
	cmd.Flags().StringVar(&owner, "owner", "", "the role that does it")
	cmd.Flags().StringVar(&after, "after", "", "the tasks it waits for: 1,2")
	cmd.Flags().StringVar(&file, "file", "", "a TOML plan of [[task]] tables; - reads stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check a --file plan and print what it would add, adding none")
	return cmd
}

// taskPlan is a [[task]] plan for `hive team task add --file`.
type taskPlan struct {
	Task []planTask `toml:"task"`
}

// planTask is one task in a plan. After names earlier refs in the file or
// numbers already on the board.
type planTask struct {
	Ref   string `toml:"ref"`
	Title string `toml:"title"`
	Owner string `toml:"owner"`
	Body  string `toml:"body"`
	After []any  `toml:"after"`
}

// readTaskPlan reads and decodes a plan, and reports the keys it doesn't know.
func readTaskPlan(path string, stdin io.Reader) (taskPlan, []string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return taskPlan{}, nil, err
	}
	var plan taskPlan
	md, err := toml.Decode(string(data), &plan)
	if err != nil {
		return taskPlan{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	var raw map[string]any
	_, _ = toml.Decode(string(data), &raw) // the typed decode already succeeded
	return plan, unknownPlanKeys(plan, raw, md), nil
}

// unknownPlanKeys words the keys the plan doesn't know. toml names a key in a
// [[task]] table without its index, so the raw decode places it in the task
// that has it.
func unknownPlanKeys(plan taskPlan, raw map[string]any, md toml.MetaData) []string {
	tasks, _ := raw["task"].([]map[string]any)
	var problems []string
	for _, key := range md.Undecoded() {
		if len(key) >= 2 && key[0] == "task" {
			name := key[len(key)-1]
			found := false
			for i, t := range tasks {
				if _, ok := t[name]; !ok {
					continue
				}
				found = true
				title := ""
				if i < len(plan.Task) {
					title = plan.Task[i].Title
				}
				problems = append(problems, fmt.Sprintf("task %d %q: unknown key %q", i+1, oneLine(title), name))
			}
			if found {
				continue
			}
		}
		problems = append(problems, fmt.Sprintf("unknown key %q", key.String()))
	}
	return problems
}

// planProblems checks a whole plan against the board's members and tasks,
// naming every problem with its task's place in the file.
func planProblems(plan taskPlan, members []store.Member, tasks []store.Task) []string {
	if len(plan.Task) == 0 {
		return []string{"the file has no [[task]]"}
	}
	var problems []string
	refAt := map[string]int{}
	for i, t := range plan.Task {
		ref := strings.TrimSpace(t.Ref)
		if ref == "" {
			continue
		}
		if j, ok := refAt[ref]; ok {
			problems = append(problems, fmt.Sprintf("task %d %q: ref %q is already used by task %d", i+1, oneLine(t.Title), ref, j+1))
			continue
		}
		refAt[ref] = i
	}
	onBoard := map[int]bool{}
	for _, t := range tasks {
		onBoard[t.N] = true
	}
	roles := team.Roles(members)
	for i, t := range plan.Task {
		label := fmt.Sprintf("task %d %q", i+1, oneLine(t.Title))
		if strings.TrimSpace(t.Title) == "" {
			problems = append(problems, label+": a task needs a title")
		}
		owner := strings.TrimPrefix(strings.TrimSpace(t.Owner), "@")
		if owner != "" && !onTeamRole(members, owner) {
			problems = append(problems, fmt.Sprintf("%s: owner %q is not on the team (roles: %s)", label, owner, roles))
		}
		for _, a := range t.After {
			s := planAfterText(a)
			if n, ok := planNumber(s); ok {
				if !onBoard[n] {
					problems = append(problems, fmt.Sprintf("%s: waits for #%d, which is not on the board", label, n))
				}
				continue
			}
			j, ok := refAt[s]
			switch {
			case ok && j >= i:
				problems = append(problems, fmt.Sprintf("%s: waits for %q, which is not an earlier task in the file", label, s))
			case !ok:
				problems = append(problems, fmt.Sprintf("%s: waits for %q, which is not an earlier ref or a task on the board", label, s))
			}
		}
	}
	return problems
}

// addTaskPlan checks and adds a whole plan, in file order, resolving refs to
// the numbers the tasks just got. It tells owners only after every task is in.
func addTaskPlan(e *teamEnv, tm store.Team, by, file string, dryRun bool, stdin io.Reader) error {
	plan, problems, err := readTaskPlan(file, stdin)
	if err != nil {
		return err
	}
	members, err := e.st.Members(tm.ID)
	if err != nil {
		return err
	}
	tasks, err := e.st.Tasks(tm.ID)
	if err != nil {
		return err
	}
	problems = append(problems, planProblems(plan, members, tasks)...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	next := 1
	for _, t := range tasks {
		next = max(next, t.N+1)
	}
	refs := map[string]int{}
	added := make([]store.Task, 0, len(plan.Task))
	for _, pt := range plan.Task {
		waits := make([]int, 0, len(pt.After))
		for _, a := range pt.After {
			s := planAfterText(a)
			if n, ok := planNumber(s); ok {
				waits = append(waits, n)
				continue
			}
			waits = append(waits, refs[s])
		}
		var task store.Task
		if dryRun {
			task = store.Task{TeamID: tm.ID, N: next, Title: strings.TrimSpace(team.Clean(pt.Title)),
				Body: strings.TrimSpace(team.Clean(pt.Body)), Owner: strings.TrimPrefix(strings.TrimSpace(pt.Owner), "@"),
				After: waits, Status: store.TaskOpen}
			next++
		} else {
			task, err = e.tr.AddTaskQuiet(tm, by, pt.Title, pt.Body, pt.Owner, waits)
			if err != nil {
				return err
			}
		}
		if ref := strings.TrimSpace(pt.Ref); ref != "" {
			refs[ref] = task.N
		}
		added = append(added, task)
	}
	// Everything is in: now the owners can hear that their tasks are ready.
	sent := make([][]tracker.Sent, len(added))
	var notifyErr error
	if !dryRun {
		for i, task := range added {
			s, err := e.tr.TellReady(tm, task, by)
			if err != nil {
				notifyErr = errors.Join(notifyErr, err)
			}
			sent[i] = s
		}
	}
	all := append(tasks, added...)
	if dryRun {
		for _, task := range added {
			fmt.Println(addWouldSaid(task, all))
		}
		fmt.Println("dry run: nothing was added")
		return nil
	}
	for i, task := range added {
		fmt.Println(addSaid(task, all, sent[i]))
	}
	return notifyErr
}

// addWouldSaid words what a dry run would do, so it can't be taken for a task
// that was really added.
func addWouldSaid(task store.Task, tasks []store.Task) string {
	line := fmt.Sprintf("#%d would be added", task.N)
	if task.Owner == "" {
		return line + ", no owner"
	}
	line += " for " + task.Owner
	if !team.Ready(task, tasks) {
		return line + " · would wait for " + team.Numbers(team.Waiting(task, tasks))
	}
	return line
}

func planAfterText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func planNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	return n, err == nil && n > 0
}

func onTeamRole(members []store.Member, role string) bool {
	for _, m := range members {
		if m.Active() && m.Role == role {
			return true
		}
	}
	return false
}

// addSaid words what happened to a new task on one line.
func addSaid(task store.Task, tasks []store.Task, sent []tracker.Sent) string {
	line := fmt.Sprintf("#%d added", task.N)
	if task.Owner == "" {
		return line + ", no owner"
	}
	line += " for " + task.Owner
	if !team.Ready(task, tasks) {
		return line + " · waits for " + team.Numbers(team.Waiting(task, tasks))
	}
	for _, s := range sent {
		if s.Role == task.Owner {
			if said := toldWording(s.How); said != "" {
				return line + " · " + said
			}
		}
	}
	return line
}

// toldWording turns what the tracker said about telling the owner into the
// words `task add` prints.
func toldWording(how string) string {
	switch {
	case how == "sent" || how == "reopened with your message":
		return "told now"
	case how == "queued; it hasn't reported in yet":
		return "told when it reports in"
	case how == "queued; the team is paused":
		return "told when the team resumes"
	case how == "queued; it is muted":
		return "told when it is unmuted"
	case how == "queued; it left the team":
		return "not told; it left the team"
	case strings.HasPrefix(how, "queued; it is "):
		return "told when it is idle (" + strings.TrimPrefix(how, "queued; it is ") + " now)"
	case how != "":
		return "told when it can take it"
	}
	return ""
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

func newTaskEditCmd(f *taskFlags) *cobra.Command {
	var title, body, owner, after string
	cmd := &cobra.Command{
		Use:   "edit <n>",
		Short: "Change a task's title, body, owner or the tasks it waits for",
		Long: "Change a task, keeping its number and status. Only the flags you pass change: the rest keep\n" +
			"the task's values. --after \"\" clears what it waits for, and --owner \"\" clears its owner.\n" +
			"--body - reads the details from stdin. The owner must be on the team, and --after must name\n" +
			"tasks on the board; a claimed task given another owner, or none, is open again.\n\n" +
			"A concurrent change wins: the edit then loses, naming who moved the task.",
		Example: "  hive team task edit 4 --owner frontend --after 1,2",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := team.ParseTask(args[0])
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
			e, tm, _, err := f.open()
			if err != nil {
				return err
			}
			defer e.st.Close()
			task, err := e.st.Task(tm.ID, n)
			if err != nil {
				return err
			}
			task, err = editValues(task, map[string]bool{
				"title": cmd.Flags().Changed("title"),
				"body":  cmd.Flags().Changed("body"),
				"owner": cmd.Flags().Changed("owner"),
				"after": cmd.Flags().Changed("after"),
			}, title, body, owner, after)
			if err != nil {
				return err
			}
			task, err = e.tr.EditTask(tm, n, task.Title, task.Body, task.Owner, task.After)
			if err != nil {
				return err
			}
			fmt.Printf("#%d edited\n", task.N)
			printTasks([]store.Task{task}, false, 0)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "the new title")
	cmd.Flags().StringVar(&body, "body", "", "the new details; - reads them from stdin")
	cmd.Flags().StringVar(&owner, "owner", "", "the new owner; \"\" clears it")
	cmd.Flags().StringVar(&after, "after", "", "the tasks it now waits for: 1,2; \"\" clears them")
	return cmd
}

func newTaskReopenCmd(f *taskFlags) *cobra.Command {
	var why string
	cmd := &cobra.Command{
		Use:   "reopen <n>",
		Short: "Send a done or blocked task back to open, and say why",
		Long: "Send a done or blocked task back to open. Its owner stays, and its note becomes the why.\n" +
			"hive posts it to the channel (to the lead, when an agent leads) and tells the owner it is\n" +
			"theirs again. An open or claimed task isn't reopened: edit or block it instead.",
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
			task, sent, err := e.tr.ReopenTask(tm, by, n, why)
			if task.N == 0 {
				return err
			}
			fmt.Printf("#%d reopened\n", task.N)
			printSent(sent)
			return err
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "why it goes back to open")
	_ = cmd.MarkFlagRequired("why")
	return cmd
}

// editValues changes only the fields the edit command was given: the rest
// keep the task's values as they were read.
func editValues(task store.Task, changed map[string]bool, title, body, owner, after string) (store.Task, error) {
	if changed["title"] {
		task.Title = title
	}
	if changed["body"] {
		task.Body = body
	}
	if changed["owner"] {
		task.Owner = owner
	}
	if changed["after"] {
		waits, err := team.ParseAfter(after)
		if err != nil {
			return store.Task{}, err
		}
		task.After = waits
	}
	return task, nil
}

func newTaskLsCmd(f *taskFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls [n]",
		Short: "List the board's tasks, with their details; give a number for one",
		Args:  cobra.MaximumNArgs(1),
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
			if len(args) > 0 {
				n, err := team.ParseTask(args[0])
				if err != nil {
					return err
				}
				i := -1
				for j := range tasks {
					if tasks[j].N == n {
						i = j
						break
					}
				}
				if i < 0 {
					return fmt.Errorf("the board has no #%d; `hive team task ls` lists the tasks", n)
				}
				tasks = tasks[i : i+1]
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(taskRows(tasks))
			}
			printTasks(tasks, true, 0)
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

// taskLines words the board: number, status, owner and title, then what done
// changed, why it is blocked, or what it waits for. A width cuts each task's
// note and body to one line; with bodies, each task's details follow it in
// full when there is no width.
func taskLines(tasks []store.Task, bodies bool, width int) []string {
	var out []string
	if len(tasks) == 0 {
		out = append(out, "  (no tasks yet; `hive team task add` adds one)")
	}
	for _, t := range tasks {
		extra := t.Note
		if w := team.Waiting(t, tasks); len(w) > 0 && !t.Closed() {
			extra = "waits for " + team.Numbers(w)
		}
		line := fmt.Sprintf("  %-4s %-8s %-10s %s", fmt.Sprintf("#%d", t.N), t.Status, team.Clean(t.Owner), team.Clean(t.Title))
		if width > 0 {
			if extra != "" {
				line += " · " + oneLine(extra)
			}
			if bodies && t.Body != "" {
				line += " · " + oneLine(t.Body)
			}
			out = append(out, truncate(line, width))
			continue
		}
		if extra != "" {
			line += " · " + team.Clean(extra)
		}
		out = append(out, line)
		if bodies && t.Body != "" {
			out = append(out, "       "+strings.ReplaceAll(team.Clean(t.Body), "\n", "\n       "))
		}
	}
	return out
}

// printTasks prints the board.
func printTasks(tasks []store.Task, bodies bool, width int) {
	for _, line := range taskLines(tasks, bodies, width) {
		fmt.Println(line)
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
