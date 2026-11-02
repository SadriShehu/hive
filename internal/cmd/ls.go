package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tree"
)

// recentWindow is how far back `hive ls` looks without --all.
const recentWindow = 24 * time.Hour

func newLsCmd() *cobra.Command {
	var all, live, asJSON, noSync bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Print the session tree",
		Long: "Print the session tree: trees with anything running, or active in the last 24h.\n" +
			"Agents can use --json to check on the sessions they spawned.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer st.Close()
			tr := newTracker(st)
			if !noSync {
				syncQuietly(cmd.Context(), tr)
			}
			sessions, err := tr.Refresh()
			if err != nil {
				return err
			}
			usages, err := tr.AllUsage()
			if err != nil {
				return err
			}
			data, err := loadTeamData(st)
			if err != nil {
				return err
			}
			if asJSON {
				roots := tree.DropEmpty(tree.Build(sessions))
				switch {
				case live:
					roots = tree.Filter(roots, func(n *tree.Node) bool { return n.Session.Live() })
				case !all:
					roots = tree.Recent(roots, time.Now().Add(-recentWindow).UnixMilli())
				}
				return printJSON(roots, usages, data.roles)
			}
			roots := tree.DropEmpty(tree.BuildTeams(sessions, data.teams, data.members))
			switch {
			case live:
				roots = tree.Filter(roots, func(n *tree.Node) bool { return n.Session.Live() })
			case !all:
				roots = tree.Recent(roots, time.Now().Add(-recentWindow).UnixMilli())
			}
			teams, rest := splitTeams(roots)
			rest = tree.DropEmpty(rest)
			if !all {
				teams = collapseDoneTeams(teams)
			}
			printTree(append(teams, rest...), usages, data, shortIDs(sessions), func(live int) string {
				return fmt.Sprintf("%d live · %d sessions", live, len(sessions)) + listedCost(append(teams, rest...), usages)
			})
			hintInstall()
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "every session ever recorded")
	cmd.Flags().BoolVarP(&live, "live", "l", false, "only running sessions and their ancestors")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: a flat list in tree order, with depth")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "skip importing history first; show only what hive already has")
	return cmd
}

// teamData is what `hive ls` needs to draw teams: the teams, their members, and
// each session's team and role.
type teamData struct {
	teams   []store.Team
	members []store.Member
	tasks   map[string][]store.Task
	roles   map[string][2]string // session ID -> team name, role
}

func loadTeamData(st *store.Store) (*teamData, error) {
	teams, err := st.Teams()
	if err != nil {
		return nil, err
	}
	members, err := st.AllMembers()
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	d := &teamData{teams: teams, members: members, tasks: map[string][]store.Task{}, roles: map[string][2]string{}}
	for _, t := range teams {
		names[t.ID] = t.Name
		if d.tasks[t.ID], err = st.Tasks(t.ID); err != nil {
			return nil, err
		}
	}
	for _, m := range members {
		if m.SessionID != "" {
			d.roles[m.SessionID] = [2]string{names[m.TeamID], m.Role}
		}
	}
	return d, nil
}

// splitTeams lifts every team node out of the tree, so the teams print first
// and the sessions that aren't on a team follow them.
func splitTeams(nodes []*tree.Node) (teams, rest []*tree.Node) {
	var walk func([]*tree.Node) []*tree.Node
	walk = func(ns []*tree.Node) []*tree.Node {
		out := make([]*tree.Node, 0, len(ns))
		for _, n := range ns {
			c := *n
			c.Children = walk(n.Children)
			if c.Team != nil {
				teams = append(teams, &c)
				continue
			}
			out = append(out, &c)
		}
		return out
	}
	rest = walk(nodes)
	return teams, rest
}

// collapseDoneTeams leaves a done team as one line, with no members.
func collapseDoneTeams(teams []*tree.Node) []*tree.Node {
	out := make([]*tree.Node, len(teams))
	for i, n := range teams {
		c := *n
		if c.Team != nil && c.Team.Status == store.TeamDone {
			c.Children = nil
		}
		out[i] = &c
	}
	return out
}

func printJSON(roots []*tree.Node, usages map[string]store.Usage, roles map[string][2]string) error {
	type row struct {
		store.Session
		Live  bool         `json:"live"`
		Depth int          `json:"depth"`
		Usage *store.Usage `json:"usage,omitempty"`
		Team  string       `json:"team,omitempty"`
		Role  string       `json:"role,omitempty"`
	}
	rows := []row{}
	var add func(nodes []*tree.Node, depth int)
	add = func(nodes []*tree.Node, depth int) {
		for _, n := range nodes {
			r := row{Session: n.Session, Live: n.Session.Live(), Depth: depth}
			r.Team, r.Role = roles[n.Session.ID][0], roles[n.Session.ID][1]
			if u, ok := usages[n.Session.ID]; ok && !u.Empty() {
				r.Usage = &u
			}
			rows = append(rows, r)
			add(n.Children, depth+1)
		}
	}
	add(roots, 0)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// lsRow is one line of the tree: a session, or a team with its members
// under it.
type lsRow struct {
	team       *store.Team
	prefix     string
	glyph      string
	glyphStyle string
	name       string
	nameStyle  string
	title      string
	titleStyle string
	kind       string
	state      string
	stateStyle string
	age        string
	cost       string
	path       string
	id         string
	summary    string // a team's members and tasks, in place of state and age
}

// memberTitle names what a member does: the task it holds, else its session's
// title, as the tree's member rows do.
func (d *teamData) memberTitle(n *tree.Node) string {
	if t, ok := team.Held(n.Role, d.tasks[n.TeamID]); ok {
		return fmt.Sprintf("#%d %s", t.N, team.Clean(t.Title))
	}
	s := n.Session
	if s.Title == "" && strings.HasPrefix(s.LastPrompt, "You are "+n.Role+" on the hive team") {
		for _, m := range d.members {
			if m.TeamID == n.TeamID && m.Role == n.Role && m.Active() && m.Prompt != "" {
				return oneLine(m.Prompt)
			}
		}
		return "(waiting for work)"
	}
	return title(s)
}

// teamLine says how many members a team has and how far its tasks got.
func (d *teamData) teamLine(t store.Team) string {
	n := 0
	for _, m := range d.members {
		if m.TeamID == t.ID && m.Active() {
			n++
		}
	}
	noun := "members"
	if n == 1 {
		noun = "member"
	}
	parts := []string{fmt.Sprintf("%d %s", n, noun)}
	if c := team.Count(d.tasks[t.ID]); c.Total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d tasks", c.Done, c.Total))
	}
	line := strings.Join(parts, " · ")
	if t.Status == store.TeamDone {
		return "done · " + line
	}
	if t.Paused {
		return "paused · " + line
	}
	return line
}

// printTree prints the teams, then the sessions not on one, then a footer
// saying how many of it are live.
func printTree(roots []*tree.Node, usages map[string]store.Usage, data *teamData, ids map[string]string, footer func(live int) string) {
	now := time.Now()
	var rows []lsRow
	liveCount := 0
	showCost := anyCost(roots, usages)
	tree.Walk(roots, func(n *tree.Node, prefix string) {
		if n.Team != nil {
			t := *n.Team
			nameStyle, glyphStyle := "1", "35"
			if t.Status == store.TeamDone {
				nameStyle, glyphStyle = "2", "2"
			}
			rows = append(rows, lsRow{
				team: &t, prefix: prefix, glyph: "⬡", glyphStyle: glyphStyle,
				name: t.Name, nameStyle: nameStyle, title: team.Clean(t.Goal), titleStyle: "2",
				summary: data.teamLine(t), stateStyle: "2",
			})
			return
		}
		s := n.Session
		live := s.Live()
		if live {
			liveCount++
		}
		glyph, glyphStyle := statusGlyph(s)
		dim := ""
		if !live {
			dim = "2"
		}
		t := title(s)
		if n.Role != "" {
			t = n.Role + " · " + data.memberTitle(n)
		}
		rows = append(rows, lsRow{
			prefix: prefix, glyph: glyph, glyphStyle: glyphStyle,
			name: s.Tool, nameStyle: toolStyle(s.Tool),
			title: t, titleStyle: dim, kind: kindTag(s),
			state: statusWord(s), stateStyle: glyphStyle,
			age: age(now, s.UpdatedAt), cost: costText(usages[s.ID]),
			path: shortPath(s.Cwd), id: shortID(ids, s.ID),
		})
	})
	if len(rows) == 0 {
		fmt.Println("no sessions yet; try `hive ls --all`, or `hive install` to start tracking")
		return
	}
	leadW, titleW, kindW, stateW, ageW, costW, pathW, idW := 0, 0, 0, 0, 0, 0, 0, 0
	for _, r := range rows {
		lead := utf8.RuneCountInString(r.prefix) + utf8.RuneCountInString(r.glyph) + 1 + utf8.RuneCountInString(r.name)
		leadW = max(leadW, lead)
		if r.team != nil {
			continue
		}
		titleW = max(titleW, utf8.RuneCountInString(r.title))
		kindW = max(kindW, utf8.RuneCountInString(r.kind))
		stateW = max(stateW, utf8.RuneCountInString(r.state))
		ageW = max(ageW, utf8.RuneCountInString(r.age))
		costW = max(costW, utf8.RuneCountInString(r.cost))
		pathW = max(pathW, utf8.RuneCountInString(r.path))
		idW = max(idW, utf8.RuneCountInString(r.id))
	}
	// Cut the title, not the state or the cost, when the terminal is narrow.
	// When even that can't fit, drop the folder and then the ID.
	width := boardWidth()
	showPath, showID := true, true
	fixed := func() int {
		n := leadW + 2 + kindW + 2 + stateW + 2 + ageW
		if showCost {
			n += 2 + costW
		}
		if showPath {
			n += 2 + pathW
		}
		if showID {
			n += 2 + idW
		}
		return n
	}
	const minTitle = 20
	if width-fixed()-4 < minTitle {
		showPath = false
	}
	if width-fixed()-4 < minTitle {
		showID = false
	}
	if room := width - fixed() - 4; titleW > room { // 4: the spaces around the title
		titleW = max(10, room)
	}
	color := useColor()
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(paint(r.prefix, "2", color))
		b.WriteString(paint(r.glyph, r.glyphStyle, color))
		b.WriteByte(' ')
		toolW := leadW - utf8.RuneCountInString(r.prefix) - utf8.RuneCountInString(r.glyph) - 1
		b.WriteString(paint(pad(r.name, toolW), r.nameStyle, color))
		b.WriteString("  ")
		if r.team != nil {
			goalW := titleW
			if r.summary != "" {
				if room := width - leadW - 2 - 2 - utf8.RuneCountInString(r.summary); goalW > room {
					goalW = max(0, room)
				}
			}
			if goalW > 0 {
				b.WriteString(paint(pad(truncate(r.title, goalW), goalW), r.titleStyle, color))
			}
			if r.summary != "" {
				if goalW > 0 {
					b.WriteString("  ")
				}
				b.WriteString(paint(r.summary, r.stateStyle, color))
			}
		} else {
			b.WriteString(paint(pad(truncate(r.title, titleW), titleW), r.titleStyle, color))
			b.WriteString("  ")
			b.WriteString(paint(pad(r.kind, kindW), "2", color))
			b.WriteString("  ")
			b.WriteString(paint(pad(r.state, stateW), r.stateStyle, color))
			b.WriteString("  ")
			b.WriteString(paint(pad(r.age, ageW), "2", color))
			if showCost {
				b.WriteString("  ")
				b.WriteString(paint(pad(r.cost, costW), "2", color))
			}
			if showPath {
				b.WriteString("  ")
				b.WriteString(paint(pad(r.path, pathW), "2", color))
			}
			if showID {
				b.WriteString("  ")
				b.WriteString(paint(r.id, "2", color))
			}
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	fmt.Println(paint(footer(liveCount), "2", color))
}

func anyCost(roots []*tree.Node, usages map[string]store.Usage) bool {
	found := false
	tree.Walk(roots, func(n *tree.Node, _ string) {
		if usages[n.Session.ID].CostSource != "" {
			found = true
		}
	})
	return found
}

func costText(u store.Usage) string {
	switch u.CostSource {
	case store.CostTool:
		return fmt.Sprintf("$%.2f", u.CostUSD)
	case store.CostConfig:
		return fmt.Sprintf("~$%.2f", u.CostUSD)
	}
	return ""
}

func listedCost(roots []*tree.Node, usages map[string]store.Usage) string {
	total, estimate := tree.Cost(roots, usages)
	if total == 0 {
		return ""
	}
	mark := ""
	if estimate {
		mark = "~"
	}
	return fmt.Sprintf(" · %s$%.2f listed", mark, total)
}

// hintInstall points at `hive install` for agents on PATH that aren't connected.
func hintInstall() {
	var missing []string
	for _, a := range adapters.All() {
		if _, err := exec.LookPath(a.Spec().New[0]); err == nil && !a.Installed() {
			missing = append(missing, a.Spec().Name)
		}
	}
	if tmux.Available() && tmux.PopupKey(tmux.ConfPath()) == "" {
		missing = append(missing, "tmux (prefix+a)")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s not connected yet: run `hive install`\n", strings.Join(missing, ", "))
	}
}

func statusGlyph(s store.Session) (string, string) {
	if !s.Live() {
		return "○", "2"
	}
	switch s.Status {
	case store.StatusWorking:
		return "●", "33"
	case store.StatusAttention:
		return "◆", "31;1"
	case store.StatusIdle:
		return "◉", "32"
	}
	return "◌", "34"
}

func statusWord(s store.Session) string {
	switch {
	case !s.Live():
		return "exited"
	case s.Status == store.StatusAttention:
		return "needs you"
	case s.Status == store.StatusUnknown && s.Source == "scan":
		return "untracked"
	case s.Status == store.StatusUnknown && s.Source == "pending":
		return "new"
	case s.Status == store.StatusUnknown:
		return "running"
	}
	return s.Status
}

func toolStyle(tool string) string {
	switch tool {
	case "claude":
		return "38;5;173"
	case "opencode":
		return "36"
	case "copilot":
		return "38;5;75"
	case "codex":
		return "38;5;35"
	}
	return "35"
}

func kindTag(s store.Session) string {
	switch s.Kind {
	case store.KindHeadless:
		return "run"
	case store.KindInternal:
		return "sub"
	}
	return ""
}

func title(s store.Session) string {
	for _, t := range []string{s.Title, s.LastPrompt} {
		if t = strings.Join(strings.Fields(t), " "); t != "" {
			return t
		}
	}
	switch s.Source {
	case "scan":
		return "(untracked)"
	case "pending", store.SourceLaunch:
		return "(new session)"
	}
	return "(untitled)"
}

// homePath writes a path under the home folder with ~.
func homePath(p string) string {
	if home := paths.Home(); strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func shortPath(p string) string {
	if p == "" {
		return ""
	}
	p = homePath(p)
	if utf8.RuneCountInString(p) <= 32 {
		return p
	}
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}

func age(now time.Time, ms int64) string {
	if ms == 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return time.UnixMilli(ms).Format("Jan 2")
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-utf8.RuneCountInString(s)))
}

func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(s, style string, color bool) string {
	if !color || style == "" || s == "" {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}
