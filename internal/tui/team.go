package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

// feedMessages is how many channel messages the feed loads.
const feedMessages = 200

// A team's channel, as loaded for the selected team node.
type feed struct {
	teamID string
	msgs   []store.Message
	err    string
}

type feedMsg struct {
	teamID string
	msgs   []store.Message
	err    error
}

// sendTarget is where the send prompt's message goes when it goes through a
// team's mailbox instead of straight to a session.
type sendTarget struct {
	team string // "" for a plain session
	to   string // a role, or team.All to address by @role
}

func (m Model) teamByID(id string) (store.Team, bool) {
	for _, t := range m.snap.Teams {
		if t.ID == id {
			return t, true
		}
	}
	return store.Team{}, false
}

// members returns a team's members that are still on it.
func (m Model) members(teamID string) []store.Member {
	var out []store.Member
	for _, mem := range m.snap.Members {
		if mem.TeamID == teamID && mem.Active() {
			out = append(out, mem)
		}
	}
	return out
}

// memberSession returns the session a member plays now.
func (m Model) memberSession(mem store.Member) (store.Session, bool) {
	if mem.SessionID == "" {
		return store.Session{}, false
	}
	s, ok := m.byID[mem.SessionID]
	return s, ok
}

func (m Model) selectedTeam() (store.Team, *tree.Node, bool) {
	n := m.selectedNode()
	if n == nil || n.TeamID == "" || m.trash {
		return store.Team{}, nil, false
	}
	t, ok := m.teamByID(n.TeamID)
	return t, n, ok
}

// memberTitle names what a member does. A session hive opened for a role
// holds the team's brief as its first prompt, which says nothing: that is
// replaced by the member's own part.
func (m Model) memberTitle(n *tree.Node) string {
	s := n.Session
	if s.Title == "" && strings.HasPrefix(s.LastPrompt, "You are "+n.Role+" on the hive team") {
		for _, mem := range m.snap.Members {
			if mem.TeamID == n.TeamID && mem.Role == n.Role && mem.Prompt != "" {
				return strings.Join(strings.Fields(mem.Prompt), " ")
			}
		}
		return "(waiting for work)"
	}
	return title(s)
}

// feedCmd loads a team's channel.
func (m *Model) feedCmd(teamID string) tea.Cmd {
	ops := m.ops
	return func() tea.Msg {
		msgs, err := ops.TeamFeed(teamID, feedMessages)
		return feedMsg{teamID: teamID, msgs: msgs, err: err}
	}
}

// onFeed takes a loaded channel, and tells the store the user has read it
// when it is on screen.
func (m *Model) onFeed(msg feedMsg) tea.Cmd {
	if n := m.selectedNode(); n == nil || n.Team == nil || n.Team.ID != msg.teamID {
		return nil
	}
	m.feed = feed{teamID: msg.teamID, msgs: msg.msgs}
	if msg.err != nil {
		m.feed.err = msg.err.Error()
		return nil
	}
	if !m.previewVisible() || len(msg.msgs) == 0 || m.snap.Unread[msg.teamID] == 0 {
		return nil
	}
	delete(m.snap.Unread, msg.teamID)
	ops, last := m.ops, msg.msgs[len(msg.msgs)-1].ID
	return func() tea.Msg {
		ops.TeamRead(msg.teamID, last)
		return nil
	}
}

// keyTeam handles the keys that mean something on a team node or on a member
// of a team. It reports whether it used the key.
func (m Model) keyTeam(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	tm, n, ok := m.selectedTeam()
	if !ok {
		return m, nil, false
	}
	isTeam := n.Team != nil
	ops := m.ops
	switch msg.String() {
	case "p":
		pause := !tm.Paused
		return m, func() tea.Msg {
			text := "resumed " + tm.Name + "; the mail that waited is delivered"
			if pause {
				text = "paused " + tm.Name + ": no mail is delivered until it resumes"
			}
			return doneMsg{text: text, err: ops.TeamPause(tm.ID, pause)}
		}, true
	case "L":
		role, text := "", "you lead "+tm.Name+" now"
		if tm.LeadRole == "" {
			role = m.agentLead(tm.ID)
			if role == "" {
				m.setFlash(tm.Name+" has no member to lead it", true)
				return m, nil, true
			}
			text = role + " leads " + tm.Name + " now"
		}
		return m, func() tea.Msg { return doneMsg{text: text, err: ops.TeamLead(tm.ID, role)} }, true
	case "m":
		m.hideSystem = !m.hideSystem
		return m, nil, true
	case "s":
		m.mode = modeSend
		m.input.Reset()
		if isTeam {
			m.sendTo = sendTarget{team: tm.ID, to: store.ToAll}
			m.sendLabel = "message everyone on " + tm.Name + " (@role for one) › "
		} else {
			m.sendTo = sendTarget{team: tm.ID, to: n.Role}
			m.sendLabel = "message " + n.Role + " on " + tm.Name + " › "
		}
		m.layoutSend()
		return m, m.input.Focus(), true
	}
	if !isTeam {
		return m, nil, false
	}
	switch msg.String() {
	case "enter":
		return m, m.jumpTeam(tm.ID), true
	case "x":
		live := 0
		for _, mem := range m.members(tm.ID) {
			if s, ok := m.memberSession(mem); ok && s.PID > 0 {
				live++
			}
		}
		if live == 0 {
			m.setFlash("nobody on "+tm.Name+" is running", true)
			return m, nil, true
		}
		m.mode, m.confirm = modeConfirm, confirmation{action: actStopTeam, team: tm, below: live}
		return m, nil, true
	case "d":
		m.setFlash("a team can't be deleted yet: x stops its members", true)
	case "c", "r", "u":
		m.setFlash("pick a member of "+tm.Name+" for that", true)
	case "y":
		name := tm.Name
		return m, func() tea.Msg { return doneMsg{text: "copied " + name, err: ops.Copy(name)} }, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

// agentLead picks the member that takes the lead back from the user: the one
// called lead, else the first.
func (m Model) agentLead(teamID string) string {
	members := m.members(teamID)
	for _, mem := range members {
		if mem.Role == team.LeadRole {
			return mem.Role
		}
	}
	if len(members) > 0 {
		return members[0].Role
	}
	return ""
}

// jumpTeam goes to the member that has needed the user longest, else the
// lead, else the first that runs.
func (m *Model) jumpTeam(teamID string) tea.Cmd {
	tm, _ := m.teamByID(teamID)
	var waiting, lead, running *store.Session
	for _, mem := range m.members(teamID) {
		s, ok := m.memberSession(mem)
		if !ok || !s.Live() {
			continue
		}
		s2 := s
		if s.Status == store.StatusAttention && (waiting == nil || s.StatusAt < waiting.StatusAt) {
			waiting = &s2
		}
		if mem.Role == tm.LeadRole {
			lead = &s2
		}
		if running == nil {
			running = &s2
		}
	}
	for _, s := range []*store.Session{waiting, lead, running} {
		if s != nil {
			return m.jump(*s)
		}
	}
	m.setFlash("nobody on "+tm.Name+" is running", true)
	return nil
}

// jumpNode is Enter on a row: a team goes to the member that needs you.
func (m *Model) jumpNode(n *tree.Node) tea.Cmd {
	if n.Team != nil {
		return m.jumpTeam(n.Team.ID)
	}
	return m.jump(n.Session)
}

// mailbox sends the send prompt's text through a team's mailbox.
func (m Model) mailbox(text string) tea.Cmd {
	target, ops := m.sendTo, m.ops
	to, body := target.to, text
	if to == store.ToAll {
		to, body = team.Address(text)
	}
	tm, _ := m.teamByID(target.team)
	return func() tea.Msg {
		how, err := ops.TeamSay(target.team, to, body)
		if err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{text: "sent to " + to + " on " + tm.Name + " · " + how}
	}
}

// ---- the new-team form

const (
	tfName = iota
	tfFolder
	tfLead
	tfMembers
	tfGoal
	teamFormFields
)

type teamForm struct {
	leads   []string // the tools that can lead, then "none"
	lead    int
	field   int
	name    textinput.Model
	folder  textinput.Model
	members textinput.Model
	goal    textarea.Model
}

func (f *teamForm) layout(w int) {
	f.name.Width = max(10, w-12)
	f.folder.Width = max(10, w-12)
	f.members.Width = max(10, w-12)
	f.goal.SetWidth(max(10, w-12))
}

func (f *teamForm) focus(field int) tea.Cmd {
	f.field = field
	f.name.Blur()
	f.folder.Blur()
	f.members.Blur()
	f.goal.Blur()
	switch field {
	case tfName:
		return f.name.Focus()
	case tfFolder:
		return f.folder.Focus()
	case tfMembers:
		return f.members.Focus()
	case tfGoal:
		return f.goal.Focus()
	}
	return nil
}

func (m Model) openTeamForm() (tea.Model, tea.Cmd) {
	tools := m.ops.Tools()
	if len(tools) == 0 {
		m.setFlash("no agent found on PATH", true)
		return m, nil
	}
	f := teamForm{leads: append(slices.Clone(tools), "none")}
	for i, t := range f.leads {
		if t == "claude" {
			f.lead = i
		}
	}
	f.name = newInput("", "api-v2")
	f.folder = newInput("", "folder")
	f.members = newInput("", "backend=claude:auto frontend=codex reviewer=opencode")
	f.goal = newPromptArea("what the team is for, and who owns what")
	dir := ""
	if s, ok := m.selected(); ok {
		dir = s.Cwd
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	f.folder.SetValue(dir)
	f.folder.CursorEnd()
	f.layout(m.formWidth())
	m.teamForm = f
	m.mode = modeTeam
	return m, m.teamForm.focus(tfName)
}

// options is what the form asks hive to start, or why it can't.
func (f teamForm) options() (tracker.TeamOptions, error) {
	o := tracker.TeamOptions{Name: strings.TrimSpace(f.name.Value()), Goal: strings.TrimSpace(f.goal.Value()),
		Cwd: strings.TrimSpace(f.folder.Value())}
	if err := team.ValidName(o.Name); err != nil {
		return o, err
	}
	specs, err := team.ParseSpecs(f.members.Value())
	if err != nil {
		return o, err
	}
	o.Members = specs
	if lead := f.leads[f.lead]; lead != "none" {
		o.Lead = &team.Spec{Role: team.LeadRole, Tool: lead}
	}
	if o.Lead == nil && len(o.Members) == 0 {
		return o, fmt.Errorf("add members, or pick a tool to lead")
	}
	return o, team.CheckRoles(append(slices.Clone(o.Members), team.Spec{Role: leadRoleOf(o)}))
}

func leadRoleOf(o tracker.TeamOptions) string {
	if o.Lead != nil {
		return o.Lead.Role
	}
	return ""
}

func (m Model) keyTeamForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.teamForm
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "tab", "down":
		return m, f.focus((f.field + 1) % teamFormFields)
	case "shift+tab", "up":
		return m, f.focus((f.field + teamFormFields - 1) % teamFormFields)
	case "left", "right":
		if f.field == tfLead {
			step := 1
			if msg.String() == "left" {
				step = -1
			}
			f.lead = (f.lead + step + len(f.leads)) % len(f.leads)
			return m, nil
		}
	case "enter":
		if f.field < tfGoal {
			return m, f.focus(f.field + 1)
		}
		opts, err := f.options()
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.mode = modeNormal
		ops := m.ops
		return m, func() tea.Msg {
			text, err := ops.TeamStart(opts)
			return doneMsg{text: text, err: err}
		}
	}
	var cmd tea.Cmd
	switch f.field {
	case tfName:
		f.name, cmd = f.name.Update(msg)
	case tfFolder:
		f.folder, cmd = f.folder.Update(msg)
	case tfMembers:
		f.members, cmd = f.members.Update(msg)
	case tfGoal:
		f.goal, cmd = f.goal.Update(msg)
	}
	return m, cmd
}

func (m Model) teamFormLines(w, h int) []string {
	f := m.teamForm
	field := func(i int, name, value string) string {
		mark := "  "
		if f.field == i {
			mark = sAccent.Render("› ")
		}
		return mark + sDim.Render(fmt.Sprintf("%-8s", name)) + value
	}
	leadLine := choices(f.leads, f.lead, func(t string) lipgloss.Style { return toolStyle(t) })
	if f.leads[f.lead] == "none" {
		leadLine += "  " + sDim.Render("you lead from the tree")
	}
	goal := visibleRows(f.goal, h-formChromeRows)
	out := []string{sBold.Render("New team"), "",
		field(tfName, "name", f.name.View()),
		field(tfFolder, "folder", f.folder.View()),
		field(tfLead, "lead", leadLine),
		field(tfMembers, "members", f.members.View()),
		field(tfGoal, "goal", goal[0]),
	}
	for _, line := range goal[1:] {
		out = append(out, strings.Repeat(" ", 10)+line)
	}
	out = append(out, "", sDim.Render("members are role=tool or role=tool:model; the model can be auto."),
		sDim.Render("each opens in its own tmux window with a brief; they report to the channel."))
	return fit(out, w, h)
}

// ---- rows and preview

// teamGlyph marks a team node by the most pressing status of its members.
func teamGlyph(n *tree.Node) (string, lipgloss.Style) {
	if !n.Live {
		return "⬡", sDim
	}
	switch n.Status {
	case store.StatusAttention:
		return "⬡", lipgloss.NewStyle().Foreground(cAttention).Bold(true)
	case store.StatusWorking:
		return "⬡", lipgloss.NewStyle().Foreground(cWorking)
	case store.StatusIdle:
		return "⬡", lipgloss.NewStyle().Foreground(cIdle)
	}
	return "⬡", lipgloss.NewStyle().Foreground(cRunning)
}

// teamRowLine draws a team node: fold mark, name, goal, how many members
// and unread posts, age.
func (m Model) teamRowLine(r row, w int, sel bool) string {
	n, t := r.node, *r.node.Team
	on := func(st lipgloss.Style) lipgloss.Style {
		if sel {
			return st.Background(cSelect)
		}
		return st
	}
	fold := "  "
	switch {
	case r.hidden > 0:
		fold = "▸ "
	case len(n.Children) > 0:
		fold = "▾ "
	}
	g, gs := teamGlyph(n)
	right := fmt.Sprintf("%d members", len(m.members(t.ID)))
	if t.Paused {
		right = "⏸ paused · " + right
	}
	if u := m.snap.Unread[t.ID]; u > 0 {
		right += fmt.Sprintf(" · %d new", u)
	}
	when := age(m.now(), n.Last)
	sp := on(lipgloss.NewStyle()).Render(" ")
	lead := ansi.StringWidth(r.prefix) + 2 + 1 + 1
	tail := 1 + ansi.StringWidth(right) + 1 + 4
	avail := max(1, w-lead-tail)
	name := ansi.Truncate(t.Name, avail, "…")
	goalW := avail - ansi.StringWidth(name) - 2
	line := on(sDim).Render(r.prefix+fold) + on(gs).Render(g) + sp + on(sBold).Render(name)
	if goalW >= 6 && t.Goal != "" {
		line += on(sDim).Render("  " + pad(ansi.Truncate(oneLine.Replace(t.Goal), goalW, "…"), goalW))
	} else {
		line += on(sDim).Render(strings.Repeat(" ", max(0, avail-ansi.StringWidth(name)+0)))
	}
	rightStyle := sDim
	if m.snap.Unread[t.ID] > 0 {
		rightStyle = sAccent
	}
	line += sp + on(rightStyle).Render(right) + sp + on(sDim).Render(fmt.Sprintf("%4s", when))
	if gap := w - ansi.StringWidth(line); gap > 0 {
		line += on(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(line, w, "")
}

// teamPreviewLines shows the team and its channel.
func (m Model) teamPreviewLines(n *tree.Node, w, h int) []string {
	t := *n.Team
	members := m.members(t.ID)
	led := "you"
	if t.LeadRole != "" {
		led = t.LeadRole
	}
	head := []string{
		sAccent.Bold(true).Render("⬡ "+t.Name) + sDim.Render(" · "+ansi.Truncate(oneLine.Replace(t.Goal), max(1, w-len(t.Name)-5), "…")),
		sDim.Render(ansi.Truncate(fmt.Sprintf("%d members · led by %s · %s · started %s ago",
			len(members), led, shortPath(t.Cwd), age(m.now(), t.CreatedAt)), w, "…")),
	}
	if t.Paused {
		head = append(head, sError.Render("⏸ paused: no mail is delivered · p resumes"))
	}
	var roster []string
	var waiting []string
	for _, mem := range members {
		s, ok := m.memberSession(mem)
		g, gs := "◌", sDim
		if ok {
			g, gs = glyph(s)
		}
		roster = append(roster, gs.Render(g)+" "+sText.Render(mem.Role))
		if ok && s.Live() && s.Status == store.StatusAttention {
			waiting = append(waiting, mem.Role)
		}
	}
	if len(roster) > 0 {
		head = append(head, strings.Join(roster, "  "))
	}
	head = append(head, sRule.Render(strings.Repeat("─", w)))

	room := max(0, h-len(head))
	var body []string
	switch {
	case m.feed.teamID != t.ID:
		body = []string{sDim.Render("…")}
	case m.feed.err != "":
		body = []string{sDim.Render(ansi.Wrap(m.feed.err, w, ""))}
	default:
		body = m.feedLines(t, w)
	}
	if len(waiting) > 0 && room > 0 {
		note := sError.Render("▸ " + strings.Join(waiting, ", ") + " waits on you · ↵ jumps there")
		body = append(body, note)
	}
	if len(body) > room {
		body = body[len(body)-room:]
	}
	return fit(append(head, body...), w, h)
}

// feedLines draws the channel: time, who, text, newest last.
func (m Model) feedLines(t store.Team, w int) []string {
	var msgs []store.Message
	for _, msg := range m.feed.msgs {
		if m.hideSystem && msg.Kind == store.MsgSystem {
			continue
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return []string{sDim.Render("nothing in the channel yet; s writes to the team")}
	}
	who := func(msg store.Message) string {
		if msg.To != "" {
			return msg.From + " → " + msg.To
		}
		return msg.From
	}
	whoW := 0
	for _, msg := range msgs {
		whoW = max(whoW, ansi.StringWidth(who(msg)))
	}
	whoW = min(whoW, max(8, w/3))
	prefixW := 6 + whoW + 1
	tools := map[string]string{}
	for _, mem := range m.members(t.ID) {
		tools[mem.Role] = mem.Tool
	}
	var out []string
	for _, msg := range msgs {
		name := ansi.Truncate(who(msg), whoW, "…")
		nameStyle, bodyStyle := sBold, sText
		switch {
		case msg.From == store.FromYou:
			nameStyle = sAccent.Bold(true)
		case msg.From == store.FromHive:
			nameStyle, bodyStyle = sDim, sDim
		case tools[msg.From] != "":
			nameStyle = toolStyle(tools[msg.From]).Bold(true)
		}
		lines := strings.Split(ansi.Wrap(msg.Body, max(10, w-prefixW), ""), "\n")
		for i, l := range lines {
			if i == 0 {
				out = append(out, sDim.Render(time.UnixMilli(msg.At).Format("15:04"))+" "+
					nameStyle.Render(pad(name, whoW))+" "+bodyStyle.Render(l))
			} else {
				out = append(out, strings.Repeat(" ", prefixW)+bodyStyle.Render(l))
			}
		}
	}
	return out
}

// teamHints are the key hints while a team or one of its members is selected.
func (m Model) teamHints() (string, bool) {
	_, n, ok := m.selectedTeam()
	if !ok {
		return "", false
	}
	if n.Team != nil {
		return keys("↵", "jump", "s", "message", "p", "pause", "L", "lead", "x", "stop", "m", "system", "T", "new team", "?", "help", "q", "quit"), true
	}
	return keys("↵", "jump", "s", "message", "p", "pause", "L", "lead", "r", "reopen", "x", "stop", "T", "new team", "?", "help", "q", "quit"), true
}

// teamCount is how many teams are still active.
func (m Model) teamCount() int {
	n := 0
	for _, t := range m.snap.Teams {
		if t.Status == store.TeamActive {
			n++
		}
	}
	return n
}

// nodeTitle is the title of the selected row: for a member, what it does.
func (m Model) nodeTitle() string {
	n := m.selectedNode()
	if n == nil {
		return ""
	}
	if n.Role != "" {
		return m.memberTitle(n)
	}
	return title(n.Session)
}
