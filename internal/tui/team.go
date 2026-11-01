package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

// feedMessages is how many channel messages the feed loads.
const feedMessages = 200

// cleanLine puts text from a teammate on one line and drops control
// characters, so the terminal draws it and runs none of it.
func cleanLine(s string) string { return oneLine.Replace(team.Clean(s)) }

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

// memberTitle names what a member does: the task it holds. Else a session
// hive opened for a role holds the team's brief as its first prompt, which
// says nothing: that is replaced by the member's own part.
func (m Model) memberTitle(n *tree.Node) string {
	if held := m.heldTitle(n.TeamID, n.Role); held != "" {
		return held
	}
	s := n.Session
	if s.Title == "" && strings.HasPrefix(s.LastPrompt, "You are "+n.Role+" on the hive team") {
		for _, mem := range m.snap.Members {
			if mem.TeamID == n.TeamID && mem.Role == n.Role && mem.Prompt != "" {
				return strings.Join(strings.Fields(team.Clean(mem.Prompt)), " ")
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
// when it is on screen: in the preview, or in the full-screen channel, even
// when the preview is hidden or the window is too narrow for it.
func (m *Model) onFeed(msg feedMsg) tea.Cmd {
	open := m.mode == modeChannel && m.channelTeam == msg.teamID
	selected := false
	if n := m.selectedNode(); n != nil && n.Team != nil && n.Team.ID == msg.teamID {
		selected = true
	}
	if !open && !selected {
		return nil
	}
	m.feed = feed{teamID: msg.teamID, msgs: msg.msgs}
	if msg.err != nil {
		m.feed.err = msg.err.Error()
		return nil
	}
	if !open && !m.previewVisible() {
		return nil
	}
	if len(msg.msgs) == 0 || m.snap.Unread[msg.teamID] == 0 {
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
		if isDone(&tm) {
			m.setFlash(tm.Name+" is done; it takes no more messages", true)
			return m, nil, true
		}
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
		m, cmd := m.openChannel(tm.ID)
		return m, cmd, true
	case "e":
		m, cmd := m.openTaskEditor(tm)
		return m, cmd, true
	case "x":
		id := tm.ID
		return m, func() tea.Msg {
			roles, err := ops.TeamRunning(id)
			return teamRunningMsg{team: tm, action: actStopTeam, roles: roles, err: err}
		}, true
	case "d":
		id := tm.ID
		return m, func() tea.Msg {
			roles, err := ops.TeamRunning(id)
			return teamRunningMsg{team: tm, action: actTrashTeam, roles: roles, err: err}
		}, true
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

// openChannel shows a team's channel full screen, with its live input line.
func (m Model) openChannel(teamID string) (Model, tea.Cmd) {
	m.mode = modeChannel
	m.channelTeam = teamID
	m.channelScroll = 0
	m.channelInput.Reset()
	m.layoutChannel()
	cmd := m.channelInput.Focus()
	return m, tea.Batch(cmd, m.feedCmd(teamID))
}

// keyChannel handles the channel: typing goes into the live line, ↵ sends it
// through the mailbox, esc goes back to the tree, and the arrows scroll the
// feed.
func (m Model) keyChannel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.channelInput.Blur()
		return m, nil
	case "enter":
		text := m.channelInput.Value()
		if strings.TrimSpace(text) == "" {
			m.channelInput.Reset()
			return m, nil
		}
		tm, ok := m.teamByID(m.channelTeam)
		if !ok {
			m.setFlash("this team is gone; esc goes back", true)
			return m, nil
		}
		if isDone(&tm) {
			m.setFlash(tm.Name+" is done; it takes no more messages", true)
			return m, nil
		}
		m.channelInput.Reset()
		m.sendTo = sendTarget{team: m.channelTeam, to: store.ToAll}
		return m, tea.Batch(m.mailbox(text), m.feedCmd(m.channelTeam))
	case "pgup", "ctrl+u":
		m.channelScroll = min(m.channelMaxScroll(), m.channelScroll+max(1, m.bodyHeight()/2))
		return m, nil
	case "pgdown", "ctrl+d":
		m.channelScroll = max(0, m.channelScroll-max(1, m.bodyHeight()/2))
		return m, nil
	case "up", "down":
		if m.channelInput.Value() != "" {
			break // the caret moves through the wrapped text
		}
		if msg.String() == "up" {
			m.channelScroll = min(m.channelMaxScroll(), m.channelScroll+1)
		} else {
			m.channelScroll = max(0, m.channelScroll-1)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.channelInput, cmd = m.channelInput.Update(msg)
	return m, cmd
}

// channelMaxScroll is how far back the feed can scroll.
func (m Model) channelMaxScroll() int {
	t, ok := m.teamByID(m.channelTeam)
	if !ok {
		return 0
	}
	return max(0, len(m.feedLines(t, m.width))-1)
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

// jumpNode is a double-click on a row: a team goes to the member that needs
// you, as it did before ↵ opened its channel.
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

// The form's fixed fields, in the order tab moves through them. Member rows
// follow tfLead, four fields each, then the goal.
const (
	tfTemplate = iota
	tfName
	tfFolder
	tfWork
	tfLead
)

const (
	memberRole = iota
	memberTool
	memberModel
	memberPart
	memberCells
)

type teamRow struct {
	role   textarea.Model
	tool   textarea.Model
	model  textarea.Model
	prompt textarea.Model
}

type teamForm struct {
	width     int
	leads     []string // the tools that can lead, then "none"
	lead      int
	work      bool // a worktree per member, not the shared folder
	templates []TeamTemplate
	template  int // 0 is none; i+1 is templates[i]
	field     int
	name      textarea.Model
	folder    textarea.Model
	goal      textarea.Model
	members   []teamRow
	models    map[string][]string // what each row's tool runs, once loaded; nil while loading
}

func newTeamRow(tool string) teamRow {
	return teamRow{
		role:   newPromptArea("role"),
		tool:   newPromptArea(tool),
		model:  newPromptArea(modelDefault),
		prompt: newPromptArea("its part"),
	}
}

func (r *teamRow) toolName() string { return strings.TrimSpace(r.tool.Value()) }

// modelChoices lists what a row's model cell cycles through: the tool's
// default, auto, then the models its tool runs.
func (f teamForm) modelChoices(row int) []string {
	return append([]string{modelDefault, fusion.Auto}, f.models[f.members[row].toolName()]...)
}

// modelChoice is where a row's model is in its choices: 0 when empty, the
// tool's default, and -1 for a name typed that isn't one of them.
func (f teamForm) modelChoice(row int) int {
	model := strings.TrimSpace(f.members[row].model.Value())
	if model == "" {
		return 0
	}
	return slices.Index(f.modelChoices(row), model)
}

// cycleModel puts the next or previous choice in a row's model cell; from a
// name typed, it starts at either end.
func (f *teamForm) cycleModel(row, step int) {
	choices := f.modelChoices(row)
	i := f.modelChoice(row)
	switch {
	case i < 0 && step > 0:
		i = 0
	case i < 0:
		i = len(choices) - 1
	default:
		i = (i + step + len(choices)) % len(choices)
	}
	model := choices[i]
	if model == modelDefault {
		model = ""
	}
	in := &f.members[row].model
	in.SetValue(model)
	in.CursorEnd()
}

func (f teamForm) loadingModels(row int) bool {
	models, asked := f.models[f.members[row].toolName()]
	return asked && models == nil
}

// teamModelsCmd loads what the focused row's tool runs, once per tool, when
// the cursor is in its model cell.
func (m *Model) teamModelsCmd() tea.Cmd {
	f := &m.teamForm
	row, cell, ok := f.rowOf(f.field)
	if !ok || cell != memberModel {
		return nil
	}
	tool := f.members[row].toolName()
	if _, asked := f.models[tool]; asked || tool == fusion.Auto || team.ValidTool(tool) != nil {
		return nil
	}
	f.models[tool] = nil
	ops := m.ops
	return func() tea.Msg {
		models, err := ops.Models(tool)
		return modelsMsg{tool: tool, models: models, err: err}
	}
}

// membersAt is the focus slot of the first member's role.
func (f teamForm) membersAt() int { return tfLead + 1 }

// goalField is the focus slot of the goal, after every member row.
func (f teamForm) goalField() int { return f.membersAt() + len(f.members)*memberCells }

// slots is how many focus slots the form has.
func (f teamForm) slots() int { return f.goalField() + 1 }

// rowOf turns a focus slot into a member row and cell, if it is one.
func (f teamForm) rowOf(field int) (row, cell int, ok bool) {
	if field < f.membersAt() || field >= f.goalField() {
		return 0, 0, false
	}
	d := field - f.membersAt()
	return d / memberCells, d % memberCells, true
}

func (r *teamRow) input(cell int) *textarea.Model {
	switch cell {
	case memberRole:
		return &r.role
	case memberTool:
		return &r.tool
	case memberModel:
		return &r.model
	}
	return &r.prompt
}

// memberWidths splits a line between the member fields; narrow terminals get
// small fields rather than overlapping text.
func memberWidths(w int) (role, tool, model, part int) {
	role = min(12, max(6, w/6))
	tool = min(12, max(6, w/6))
	model = min(14, max(6, w/6))
	part = max(6, w-(7+role+tool+model))
	return
}

func (f *teamForm) layout(w int) {
	f.width = w
	f.name.SetWidth(max(10, w-12))
	f.folder.SetWidth(max(10, w-12))
	f.goal.SetWidth(max(10, w-12))
	role, tool, model, part := memberWidths(w)
	for i := range f.members {
		f.members[i].role.SetWidth(role)
		f.members[i].tool.SetWidth(tool)
		f.members[i].model.SetWidth(model)
		f.members[i].prompt.SetWidth(part)
	}
}

func (f *teamForm) focus(field int) tea.Cmd {
	f.field = max(0, min(field, f.slots()-1))
	f.name.Blur()
	f.folder.Blur()
	f.goal.Blur()
	for i := range f.members {
		for cell := range memberCells {
			f.members[i].input(cell).Blur()
		}
	}
	switch f.field {
	case tfName:
		return f.name.Focus()
	case tfFolder:
		return f.folder.Focus()
	}
	if r, cell, ok := f.rowOf(f.field); ok {
		return f.members[r].input(cell).Focus()
	}
	if f.field == f.goalField() {
		return f.goal.Focus()
	}
	return nil
}

// addMember appends a blank member row and puts the cursor in its role.
func (f *teamForm) addMember() {
	f.members = append(f.members, newTeamRow("claude"))
	f.layout(f.width)
	f.field = f.membersAt() + (len(f.members)-1)*memberCells
}

// removeMember removes the row the cursor is on, or the last one, but never
// the only row.
func (f *teamForm) removeMember() {
	if len(f.members) <= 1 {
		return
	}
	row := len(f.members) - 1
	if r, _, ok := f.rowOf(f.field); ok {
		row = r
	}
	oldGoal := f.goalField()
	f.members = append(f.members[:row], f.members[row+1:]...)
	switch {
	case f.field >= oldGoal:
		f.field -= memberCells
	case f.field >= f.membersAt():
		f.field = min(f.field, f.goalField()-1)
	}
}

func (m Model) openTeamForm() (tea.Model, tea.Cmd) {
	tools := m.ops.Tools()
	if len(tools) == 0 {
		m.setFlash("no agent found on PATH", true)
		return m, nil
	}
	f := teamForm{leads: append(slices.Clone(tools), "none"), templates: m.ops.TeamTemplates(), models: map[string][]string{}}
	for i, t := range f.leads {
		if t == "claude" {
			f.lead = i
		}
	}
	f.name = newPromptArea("api-v2")
	f.folder = newPromptArea("folder")
	f.goal = newPromptArea("what the team is for, and who owns what")
	f.members = []teamRow{newTeamRow("claude")}
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

// templateLine draws the template row: none, or a saved roster.
func (f teamForm) templateLine() string {
	names := []string{"none"}
	for _, t := range f.templates {
		names = append(names, t.Name)
	}
	line := choices(names, f.template, func(string) lipgloss.Style { return sAccent })
	switch {
	case f.template > 0:
		line += "  " + sDim.Render("fills the form; the name stays yours")
	case len(f.templates) == 0:
		line += "  " + sDim.Render("add a [[team]] table to config.toml")
	}
	return line
}

// applyTemplate fills the form from the selected template, leaving the team
// name alone: that is still typed for this run.
func (f *teamForm) applyTemplate() {
	if f.template <= 0 || f.template > len(f.templates) {
		return
	}
	t := f.templates[f.template-1]
	if t.Cwd != "" {
		f.folder.SetValue(t.Cwd)
		f.folder.CursorEnd()
	}
	f.work = t.Worktrees
	if t.Goal != "" {
		f.goal.SetValue(t.Goal)
	}
	if t.Lead != "" {
		f.setLead(t.Lead)
	}
	f.members = nil
	for _, m := range t.Members {
		row := newTeamRow("claude")
		row.role.SetValue(m.Role)
		row.tool.SetValue(m.Tool)
		row.model.SetValue(m.Model)
		row.prompt.SetValue(m.Prompt)
		f.members = append(f.members, row)
	}
	if len(f.members) == 0 {
		f.members = []teamRow{newTeamRow("claude")}
	}
	f.layout(f.width)
}

// setLead picks a tool in the lead row, adding it when a template names one
// that isn't installed here.
func (f *teamForm) setLead(lead string) {
	if len(f.leads) == 0 {
		f.leads = []string{"none"}
	}
	tool, _, _ := strings.Cut(lead, ":")
	if tool == "" {
		tool = "none"
	}
	f.lead = slices.Index(f.leads, tool)
	if f.lead >= 0 {
		return
	}
	f.leads = append(f.leads[:len(f.leads)-1], tool, "none")
	f.lead = len(f.leads) - 2
}

// options is what the form asks hive to start, or why it can't.
func (f teamForm) options() (tracker.TeamOptions, error) {
	o := tracker.TeamOptions{Name: strings.TrimSpace(f.name.Value()), Goal: strings.TrimSpace(f.goal.Value()),
		Cwd: strings.TrimSpace(f.folder.Value()), Worktrees: f.work}
	if err := team.ValidName(o.Name); err != nil {
		return o, err
	}
	var specs []team.Spec
	seen := map[string]bool{}
	for i, row := range f.members {
		role := strings.TrimSpace(row.role.Value())
		tool := strings.TrimSpace(row.tool.Value())
		if role == "" || tool == "" {
			return o, fmt.Errorf("member row %d needs a role and a tool", i+1)
		}
		if err := team.ValidRole(role); err != nil {
			return o, fmt.Errorf("member row %d: %w", i+1, err)
		}
		if err := team.ValidTool(tool); err != nil {
			return o, fmt.Errorf("member row %d: %w", i+1, err)
		}
		if seen[role] {
			return o, fmt.Errorf("member row %d: the role %q is on the team twice", i+1, role)
		}
		seen[role] = true
		specs = append(specs, team.Spec{Role: role, Tool: tool,
			Model: strings.TrimSpace(row.model.Value()), Prompt: strings.TrimSpace(row.prompt.Value())})
	}
	o.Members = specs
	lead := "none"
	if f.lead >= 0 && f.lead < len(f.leads) {
		lead = f.leads[f.lead]
	}
	if lead != "none" {
		o.Lead = &team.Spec{Role: team.LeadRole, Tool: lead}
	}
	all := slices.Clone(o.Members)
	if o.Lead != nil {
		all = append(all, *o.Lead)
	}
	if err := team.CheckRoles(all); err != nil {
		return o, err
	}
	return o, nil
}

// keyTeamForm edits the new-team form, and loads what a row's tool runs
// once the cursor is in its model cell.
func (m Model) keyTeamForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	next, cmd := m.editTeamForm(msg)
	nm, ok := next.(Model)
	if !ok || nm.mode != modeTeam {
		return next, cmd
	}
	load := nm.teamModelsCmd()
	return nm, tea.Batch(cmd, load)
}

func (m Model) editTeamForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.teamForm
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "tab":
		return m, f.focus((f.field + 1) % f.slots())
	case "shift+tab":
		return m, f.focus((f.field + f.slots() - 1) % f.slots())
	case "up", "down":
		// In a field that holds a lot of text, up and down move the caret
		// through its wrapped lines; tab moves between fields.
		if a := f.longField(); a == nil || a.Value() == "" {
			step := 1
			if msg.String() == "up" {
				step = -1
			}
			return m, f.focus((f.field + step + f.slots()) % f.slots())
		}
	case "+":
		f.addMember()
		return m, f.focus(f.field)
	case "ctrl+d":
		f.removeMember()
		return m, f.focus(f.field)
	case "left", "right":
		step := 1
		if msg.String() == "left" {
			step = -1
		}
		switch f.field {
		case tfTemplate:
			f.template = (f.template + step + len(f.templates) + 1) % (len(f.templates) + 1)
			f.applyTemplate()
			return m, nil
		case tfLead:
			f.lead = (f.lead + step + len(f.leads)) % len(f.leads)
			return m, nil
		case tfWork:
			f.work = !f.work
			return m, nil
		}
		if row, cell, ok := f.rowOf(f.field); ok && cell == memberModel {
			f.cycleModel(row, step)
			return m, nil
		}
	case "enter":
		if f.field < f.goalField() {
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
	}
	if r, cell, ok := f.rowOf(f.field); ok {
		in := f.members[r].input(cell)
		*in, cmd = in.Update(msg)
	}
	if f.field == f.goalField() {
		f.goal, cmd = f.goal.Update(msg)
	}
	return m, cmd
}

// memberCellRows is the most rows one member cell draws before its text
// scrolls with the caret.
const memberCellRows = 4

// memberHeight is how many lines a member's row takes.
func (f teamForm) memberHeight(i, w int) int {
	role, tool, model, part := memberWidths(w)
	row := &f.members[i]
	height := 1
	for _, c := range []struct {
		area  textarea.Model
		width int
	}{{row.role, role}, {row.tool, tool}, {row.model, model}, {row.prompt, part}} {
		height = max(height, len(visibleRows(c.area, memberCellRows)))
	}
	return height
}

// memberWindow is the member rows to draw: as many as fit in room lines,
// keeping the focused row on screen.
func (f teamForm) memberWindow(room, w int) (start, end int) {
	n := len(f.members)
	if n == 0 {
		return 0, 0
	}
	room = max(1, room)
	focus := 0
	if r, _, ok := f.rowOf(f.field); ok {
		focus = r
	}
	start, end = focus, focus+1
	used := f.memberHeight(focus, w)
	for end < n && used+f.memberHeight(end, w) <= room {
		used += f.memberHeight(end, w)
		end++
	}
	for start > 0 && used+f.memberHeight(start-1, w) <= room {
		used += f.memberHeight(start-1, w)
		start--
	}
	return start, end
}

// memberLines draws one member's row: its cells side by side, each wrapped
// within its own column when it is long.
func (f teamForm) memberLines(i, w int) []string {
	role, tool, model, part := memberWidths(w)
	row := &f.members[i]
	cells := []struct {
		area  textarea.Model
		width int
	}{{row.role, role}, {row.tool, tool}, {row.model, model}, {row.prompt, part}}
	height := 1
	views := make([][]string, len(cells))
	for j, c := range cells {
		views[j] = visibleRows(c.area, memberCellRows)
		height = max(height, len(views[j]))
	}
	mark := "  "
	if r, _, ok := f.rowOf(f.field); ok && r == i {
		mark = sAccent.Render("› ")
	}
	number := sDim.Render(fmt.Sprintf("%d ", i+1))
	indent := strings.Repeat(" ", ansi.StringWidth(mark)+ansi.StringWidth(number))
	out := make([]string, height)
	for k := range height {
		var b strings.Builder
		if k == 0 {
			b.WriteString(mark + number)
		} else {
			b.WriteString(indent)
		}
		for j, c := range cells {
			line := ""
			if k < len(views[j]) {
				line = views[j][k]
			}
			b.WriteString(pad(ansi.Truncate(line, c.width, ""), c.width))
			if j < len(cells)-1 {
				b.WriteString(" ")
			}
		}
		out[k] = b.String()
	}
	return out
}

// longField is the focused field that holds a lot of text, whose caret up and
// down move through; nil when the focus is a table cell or a choice.
func (f *teamForm) longField() *textarea.Model {
	switch f.field {
	case tfName:
		return &f.name
	case tfFolder:
		return &f.folder
	}
	if f.field == f.goalField() {
		return &f.goal
	}
	return nil
}

func (m Model) teamFormLines(w, h int) []string {
	f := m.teamForm
	mark := func(i int) string {
		if f.field == i {
			return sAccent.Render("› ")
		}
		return "  "
	}
	single := func(i int, name, value string) string {
		return mark(i) + sDim.Render(fmt.Sprintf("%-8s", name)) + value
	}
	leadLine := choices(f.leads, f.lead, func(t string) lipgloss.Style { return toolStyle(t) })
	if f.leads[f.lead] == "none" {
		leadLine += "  " + sDim.Render("you lead from the tree")
	}
	goalLines := 3
	if h < 20 {
		goalLines = 1
	}
	nameRows := fieldRows(f.name, 3)
	folderRows := fieldRows(f.folder, 3)
	role, tool, model, _ := memberWidths(w)
	out := []string{sBold.Render("New team"), "",
		single(tfTemplate, "template", f.templateLine()),
	}
	out = append(out, fieldLines(mark(tfName), "name", f.name, 8, w, nameRows)...)
	out = append(out, fieldLines(mark(tfFolder), "folder", f.folder, 8, w, folderRows)...)
	out = append(out, single(tfWork, "work", f.workLine()), single(tfLead, "lead", leadLine),
		"  "+sDim.Render(pad("role", role)+" "+pad("tool", tool)+" "+pad("model", model)+" part"))
	// The member rows take what is left above the goal and the help.
	after := 1 + goalLines + 1 + 2
	room := max(1, h-len(out)-after)
	start, end := f.memberWindow(room, w)
	for i := start; i < end; i++ {
		out = append(out, f.memberLines(i, w)...)
	}
	out = append(out, "")
	out = append(out, fieldLines(mark(f.goalField()), "goal", f.goal, 8, w, goalLines)...)
	help := []string{sDim.Render("+ adds a member row, ctrl+d removes the focused one; roles and tools are required."),
		sDim.Render("each member opens in its own tmux window with a brief; they report to the channel.")}
	if row, cell, ok := f.rowOf(f.field); ok && cell == memberModel {
		help = f.modelHelp(row, w)
	}
	out = append(append(out, ""), help...)
	return fit(out, w, h)
}

// modelHelp is the help while the cursor is in a row's model cell: the
// choices ←/→ moves through, around the one in the cell, and what else
// the cell takes.
func (f teamForm) modelHelp(row, w int) []string {
	tool := f.members[row].toolName()
	if tool == fusion.Auto {
		return []string{sDim.Render("tool auto picks the model too, for the goal and the member's part."),
			sDim.Render("name a tool to pick its model here.")}
	}
	items, at := f.modelChoices(row), f.modelChoice(row)
	shown := func(from int) string {
		line := choices(items[from:], at-from, func(string) lipgloss.Style { return sAccent })
		if from > 0 {
			line = sDim.Render("…") + "  " + line
		}
		return line
	}
	from := 0
	for from < at && ansi.StringWidth(shown(from)) > w {
		from++
	}
	line := shown(from)
	if f.loadingModels(row) {
		line += "  " + sDim.Render("loading what it runs…")
	}
	return []string{line, sDim.Render("←/→ picks one, or type an ID or a name like opus 5.5.")}
}

// ---- rows and preview

// teamGlyph marks a team node by the most pressing status of its members.
func teamGlyph(n *tree.Node) (string, lipgloss.Style) {
	if !n.Live || isDone(n.Team) {
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
	if tasks := m.taskCount(t.ID); tasks != "" {
		right += " · " + tasks
	}
	if _, trashed := m.trashedTeamByID(t.ID); trashed {
		right = "in the trash · " + right
	} else if t.Paused {
		right = "⏸ paused · " + right
	}
	nameStyle := sBold
	if isDone(&t) {
		right, nameStyle = "✓ done · "+fmt.Sprintf("%d members", len(m.members(t.ID))), sDim
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
	line := on(sDim).Render(r.prefix+fold) + on(gs).Render(g) + sp + on(nameStyle).Render(name)
	if goalW >= 6 && t.Goal != "" {
		line += on(sDim).Render("  " + pad(ansi.Truncate(cleanLine(t.Goal), goalW, "…"), goalW))
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

// teamHead draws who a team is: its name and goal, how it runs, its state and
// its roster.
func (m Model) teamHead(t store.Team, w int) []string {
	members := m.members(t.ID)
	led := "you"
	if t.LeadRole != "" {
		led = t.LeadRole
	}
	where := shortPath(t.Cwd)
	if t.Workspace == store.WorkspaceWorktrees {
		where += " · a worktree per member"
	}
	head := []string{sAccent.Bold(true).Render("⬡ " + t.Name)}
	if goal := strings.TrimSpace(cleanLine(t.Goal)); goal != "" {
		for i, line := range strings.Split(ansi.Wrap(goal, max(10, w-4), ""), "\n") {
			prefix := "    "
			if i == 0 {
				prefix = "  " + sDim.Render("· ")
			}
			head = append(head, prefix+sDim.Render(line))
		}
	}
	head = append(head, sDim.Render(ansi.Truncate(fmt.Sprintf("%d members · led by %s · %s · started %s ago",
		len(members), led, where, age(m.now(), t.CreatedAt)), w, "…")))
	if _, trashed := m.trashedTeamByID(t.ID); trashed {
		head = append(head, sError.Render("in the trash · r restores it · d deletes it for good"))
	} else if isDone(&t) {
		head = append(head, sDim.Render("✓ done: the goal is met; the team takes no more messages"))
	} else if t.Paused {
		head = append(head, sError.Render("⏸ paused: no mail is delivered · p resumes"))
	}
	tools := map[string]bool{}
	for _, mem := range members {
		s, ok := m.memberSession(mem)
		g, gs := "◌", sDim
		state := "starting"
		if ok {
			g, gs = glyph(s)
			state = s.Status
			if s.Status == store.StatusAttention {
				state = "needs you"
			}
		}
		line := sText.Render(mem.Role) + sDim.Render(" · "+state)
		if u, ok := m.usageFor(mem.SessionID); ok {
			if u.Model != "" {
				line += sDim.Render(" · ") + sText.Render(u.Model)
			}
			if u.Effort != "" {
				line += sText.Render(" " + u.Effort)
			}
			if context := contextLine(u, w, 10); context != "" {
				line += sDim.Render(" · ") + context
			}
		} else if mem.Model != "" && mem.Model != fusion.Auto {
			line += sDim.Render(" · " + mem.Model)
		}
		if id := shortSessionID(mem.SessionID); id != "" {
			line += sDim.Render(" · " + id)
		}
		// The member's part is long text: it wraps under its role.
		if part := strings.TrimSpace(cleanLine(mem.Prompt)); part != "" {
			line += sDim.Render(" · ") + sText.Render(part)
		}
		for i, wrapped := range strings.Split(ansi.Wrap(line, max(10, w-4), ""), "\n") {
			prefix := "    "
			if i == 0 {
				prefix = "  " + gs.Render(g) + " "
			}
			head = append(head, prefix+wrapped)
		}
		tools[mem.Tool] = true
	}
	// Rate limits are the account's, not one member's: show them once per tool.
	for _, tool := range sortedKeys(tools) {
		if q, ok := m.quotas[tool]; ok {
			if text := quotaText(q, m.now()); text != "" {
				head = append(head, "  "+sDim.Render(tool+" ")+text)
			}
		}
	}
	return head
}

// sortedKeys lists a set's keys in order, so the lines don't jump around.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// teamPreviewLines shows the team and its channel.
func (m Model) teamPreviewLines(n *tree.Node, w, h int) []string {
	t := *n.Team
	members := m.members(t.ID)
	var waiting []string
	for _, mem := range members {
		s, ok := m.memberSession(mem)
		if ok && s.Live() && s.Status == store.StatusAttention {
			waiting = append(waiting, mem.Role)
		}
	}
	head := m.teamHead(t, w)
	head = append(head, sRule.Render(strings.Repeat("─", w)))
	// The board takes up to half of what is left; the channel the rest.
	if tasks := m.taskLines(t.ID, w, max(2, (h-len(head))/2)); len(tasks) > 0 {
		head = append(append(head, tasks...), sRule.Render(strings.Repeat("─", w)))
	}

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
		note := sError.Render("▸ " + strings.Join(waiting, ", ") + " waits on you · its row's ↵ jumps there")
		body = append(body, note)
	}
	if len(body) > room {
		body = body[len(body)-room:]
	}
	return fit(append(head, body...), w, h)
}

// channelLines draws the full-screen channel: who the team is, its board and
// then the feed, scrolled back from the newest post with ↑/↓.
func (m Model) channelLines(w, h int) []string {
	t, ok := m.teamByID(m.channelTeam)
	if !ok {
		return fit([]string{sDim.Render("this team is gone; esc goes back to the tree")}, w, h)
	}
	head := m.teamHead(t, w)
	head = append(head, sRule.Render(strings.Repeat("─", w)))
	// The board takes up to a third of the room; the feed the rest.
	room := max(0, h-len(head))
	if room > 1 {
		if tasks := m.taskLines(t.ID, w, max(1, room/3)); len(tasks) > 0 {
			head = append(head, tasks...)
			head = append(head, sRule.Render(strings.Repeat("─", w)))
		}
	}
	room = max(0, h-len(head))
	var body []string
	switch {
	case m.feed.teamID != t.ID:
		body = []string{sDim.Render("…")}
	case m.feed.err != "":
		body = []string{sDim.Render(ansi.Wrap(m.feed.err, w, ""))}
	default:
		body = m.feedLines(t, w)
	}
	if len(body) > room {
		if m.channelScroll > 0 {
			end := max(0, len(body)-m.channelScroll)
			start := max(0, end-room)
			body = body[start:end]
		} else {
			body = body[len(body)-room:]
		}
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
			return team.Clean(msg.From) + " → " + team.Clean(msg.To)
		}
		return team.Clean(msg.From)
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
		lines := strings.Split(ansi.Wrap(team.Clean(msg.Body), max(10, w-prefixW), ""), "\n")
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
		return keys("↵", "channel", "s", "message", "p", "pause", "L", "lead", "x", "stop", "m", "system", "T", "new team", "?", "help", "q", "quit"), true
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
