// Package tui is hive's terminal UI: the session tree, a live preview of the
// selected session, and the keys to jump into, message, start, reopen, stop
// and delete agents; and the trash, to restore sessions or delete them for
// good.
package tui

import (
	"context"
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

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

const (
	refreshEvery = 1500 * time.Millisecond
	syncEvery    = 30 * time.Second
	recentWindow = 24 * time.Hour
	flashFor     = 6 * time.Second
	previewLines = 80 // transcript entries fetched for the preview

	promptAreaRows = 200
	formChromeRows = 7
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeSend
	modeNew
	modeConfirm
	modeHelp
)

// Model is the TUI state.
type Model struct {
	ops   Ops
	popup bool // opened as a tmux popup: close once the user has gone somewhere
	now   func() time.Time

	width, height int

	sessions []store.Session
	trashed  []store.Session
	byID     map[string]store.Session // both of them
	rows     []row
	cursor   int
	offset   int
	selID    string

	collapsed map[string]bool
	showAll   bool
	hideSubs  bool
	trash     bool // the trash shows instead of the tree
	filter    textinput.Model

	mode      mode
	input     textarea.Model // the message being sent
	sendLabel string
	form      form
	confirm   confirmation

	showPreview bool
	preview     preview

	flash    string
	flashErr bool
	flashAt  time.Time

	loaded     bool
	refreshing bool
	syncing    bool
	lastSync   time.Time
}

type row struct {
	node   *tree.Node
	prefix string
	hidden int // sessions folded away under this one
}

type preview struct {
	id      string
	screen  []string     // what its tmux pane shows
	entries []agent.Line // or the end of its transcript
	err     string
}

// Actions that ask first.
const (
	actStop  = "stop"
	actTrash = "trash"
	actPurge = "purge"
)

// confirmation is an action waiting for y.
type confirmation struct {
	action  string
	session store.Session
	below   int // sessions under it that go with it
}

// under returns every session below id in sessions, at any depth.
func under(sessions []store.Session, id string) []store.Session {
	children := map[string][]store.Session{}
	for _, s := range sessions {
		if s.ParentID != "" {
			children[s.ParentID] = append(children[s.ParentID], s)
		}
	}
	var out []store.Session
	seen := map[string]bool{id: true}
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		for _, c := range children[queue[0]] {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c)
				queue = append(queue, c.ID)
			}
		}
	}
	return out
}

func belowText(below int) string {
	switch below {
	case 0:
		return ""
	case 1:
		return " and the 1 session under it"
	}
	return fmt.Sprintf(" and the %d sessions under it", below)
}

// form is the new-agent form.
type form struct {
	parent store.Session // zero for a top-level agent
	tools  []string
	tool   int
	field  int // 0 tool, 1 folder, 2 prompt
	folder textinput.Model
	prompt textarea.Model

	folders []string // suggestions that complete the folder being typed
	pick    int      // the highlighted one; -1 keeps what's typed
}

// suggest lists the folders that complete the folder field, none highlighted.
func (f *form) suggest() {
	f.folders, f.pick = folderSuggestions(f.folder.Value()), -1
}

// keyFolders works the folder suggestions: ↑/↓ highlight one, tab fills it
// in (the first if none is) and lists the folders inside it, enter takes it
// and moves on, esc hides the list until the folder is edited again. It
// reports whether it used the key.
func (f *form) keyFolders(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up":
		f.pick = max(-1, f.pick-1)
	case "down":
		f.pick = min(len(f.folders)-1, f.pick+1)
	case "tab":
		f.folder.SetValue(f.folders[max(0, f.pick)] + "/")
		f.folder.CursorEnd()
		f.suggest()
	case "enter":
		if f.pick < 0 {
			return nil, false
		}
		f.folder.SetValue(f.folders[f.pick])
		f.folder.CursorEnd()
		return f.focus(2), true
	case "esc":
		f.folders = nil
	default:
		return nil, false
	}
	return nil, true
}

func (f *form) layout(w int) {
	f.folder.Width = max(10, w-12)
	f.prompt.SetWidth(max(10, w-12))
}

func wrappedRows(area textarea.Model) int {
	width := max(1, area.Width())
	rows := 0
	for i, line := range strings.Split(area.Value(), "\n") {
		if i == area.Line() {
			rows += area.LineInfo().Height
			continue
		}
		rows += rowsWithSlack(line, width)
	}
	return rows
}

func rowsWithSlack(line string, width int) int {
	return strings.Count(ansi.Wrap(line, width, ""), "\n") + 2
}

func cursorRow(area textarea.Model) int {
	width := max(1, area.Width())
	row := 0
	for i, line := range strings.Split(area.Value(), "\n") {
		if i == area.Line() {
			return row + area.LineInfo().RowOffset
		}
		row += rowsWithSlack(line, width)
	}
	return row
}

func visibleRows(area textarea.Model, limit int) []string {
	lines := strings.Split(area.View(), "\n")
	rows := min(len(lines), wrappedRows(area))
	limit = max(1, limit)
	if rows <= limit {
		return lines[:rows]
	}
	start := min(max(0, cursorRow(area)-limit+1), rows-limit)
	return lines[start : start+limit]
}

type (
	tickMsg      time.Time
	refreshedMsg struct {
		sessions, trashed []store.Session
		err               error
	}
	syncedMsg  struct{ err error }
	previewMsg struct {
		id      string
		screen  []string
		entries []agent.Line
		err     error
	}
	doneMsg struct {
		text string
		err  error
		quit bool
	}
)

// New returns the TUI model.
func New(ops Ops, popup bool) Model {
	m := Model{ops: ops, popup: popup, now: time.Now, collapsed: map[string]bool{}, showPreview: true}
	m.filter = newInput("/ ", "filter by title, folder or ID")
	m.input = newPromptArea("message")
	return m
}

func (m *Model) layoutSend() {
	label := m.sendLabel
	m.input.SetPromptFunc(ansi.StringWidth(label), func(line int) string {
		if line == 0 {
			return label
		}
		return ""
	})
	m.input.SetWidth(max(ansi.StringWidth(label)+10, m.width-1))
	m.clampScroll()
}

func (m Model) sendRows() int {
	if m.mode != modeSend {
		return 0
	}
	return min(wrappedRows(m.input), max(1, m.height-7))
}

func newInput(prompt, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.Placeholder = placeholder
	in.PromptStyle = sAccent
	in.PlaceholderStyle = sDim
	in.CharLimit = 16000
	return in
}

func newPromptArea(placeholder string) textarea.Model {
	area := textarea.New()
	area.Prompt = ""
	area.ShowLineNumbers = false
	area.Placeholder = placeholder
	area.CharLimit = 16000
	area.MaxHeight = 0
	plain := lipgloss.NewStyle()
	for _, style := range []*textarea.Style{&area.FocusedStyle, &area.BlurredStyle} {
		style.Base, style.CursorLine, style.Text, style.EndOfBuffer = plain, plain, plain, plain
		style.Placeholder = sDim
		style.Prompt = sAccent
	}
	area.SetHeight(promptAreaRows)
	return area
}

// Run shows the TUI until the user quits.
func Run(ops Ops, popup bool) error {
	_, err := tea.NewProgram(New(ops, popup), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.syncCmd(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) refreshCmd() tea.Cmd {
	m.refreshing = true
	ops := m.ops
	return func() tea.Msg {
		s, err := ops.Refresh()
		if err != nil {
			return refreshedMsg{err: err}
		}
		trashed, err := ops.Trashed()
		return refreshedMsg{sessions: s, trashed: trashed, err: err}
	}
}

func (m *Model) syncCmd() tea.Cmd {
	m.syncing = true
	m.lastSync = m.now()
	ops := m.ops
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return syncedMsg{ops.Sync(ctx)}
	}
}

// previewCmd loads what the selected session shows: its pane's screen when
// it runs in one, else the end of its transcript.
func (m *Model) previewCmd() tea.Cmd {
	s, ok := m.selected()
	if !ok {
		return nil
	}
	ops := m.ops
	pane := m.paneFor(s)
	own := pane != "" && pane == s.Pane
	return func() tea.Msg {
		if own {
			if screen, err := ops.Capture(pane); err == nil {
				return previewMsg{id: s.ID, screen: strings.Split(screen, "\n")}
			}
		}
		entries, err := ops.Tail(s, previewLines)
		return previewMsg{id: s.ID, entries: entries, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		if m.mode == modeNew {
			m.form.layout(m.formWidth())
		}
		if m.mode == modeSend {
			m.layoutSend()
		}
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tick(), m.previewCmd()}
		if !m.refreshing {
			cmds = append(cmds, m.refreshCmd())
		}
		if !m.syncing && m.now().Sub(m.lastSync) > syncEvery {
			cmds = append(cmds, m.syncCmd())
		}
		if m.flash != "" && m.now().Sub(m.flashAt) > flashFor {
			m.flash = ""
		}
		return m, tea.Batch(cmds...)

	case refreshedMsg:
		m.refreshing = false
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return m, nil
		}
		first := !m.loaded
		m.loaded = true
		m.sessions, m.trashed = msg.sessions, msg.trashed
		m.byID = make(map[string]store.Session, len(msg.sessions)+len(msg.trashed))
		for _, s := range append(slices.Clip(msg.trashed), msg.sessions...) {
			m.byID[s.ID] = s
		}
		m.rebuild()
		if first {
			return m, m.previewCmd()
		}
		return m, nil

	case syncedMsg:
		m.syncing = false
		if msg.err != nil {
			m.setFlash("sync: "+msg.err.Error(), true)
		}
		if m.refreshing {
			return m, nil
		}
		return m, m.refreshCmd()

	case previewMsg:
		if msg.id == m.selID {
			m.preview = preview{id: msg.id, screen: msg.screen, entries: msg.entries}
			if msg.err != nil {
				m.preview.err = msg.err.Error()
			}
		}
		return m, nil

	case doneMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return m, nil
		}
		if msg.quit {
			return m, tea.Quit
		}
		m.setFlash(msg.text, false)
		return m, m.refreshCmd()

	case tea.MouseMsg:
		return m.mouse(msg)

	case tea.KeyMsg:
		switch m.mode {
		case modeFilter:
			return m.keyFilter(msg)
		case modeSend:
			return m.keySend(msg)
		case modeNew:
			return m.keyNew(msg)
		case modeConfirm:
			return m.keyConfirm(msg)
		case modeHelp:
			m.mode = modeNormal
			return m, nil
		}
		if m.trash {
			return m.keyTrash(msg)
		}
		return m.keyNormal(msg)
	}
	return m, nil
}

func (m *Model) setFlash(text string, isErr bool) {
	m.flash, m.flashErr, m.flashAt = text, isErr, m.now()
}

func (m Model) keyNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s, ok := m.selected()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.rebuild()
			return m, m.previewCmd()
		}
		if m.popup {
			return m, tea.Quit
		}
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "pgup", "ctrl+u":
		return m.move(-max(1, m.bodyHeight()-1))
	case "pgdown", "ctrl+d":
		return m.move(max(1, m.bodyHeight()-1))
	case "home", "g":
		return m.move(-len(m.rows))
	case "end", "G":
		return m.move(len(m.rows))
	case "left", "h":
		if ok && len(m.rows[m.cursor].node.Children) > 0 && !m.collapsed[s.ID] {
			m.collapsed[s.ID] = true
			m.rebuild()
			return m, nil
		}
		if ok && s.ParentID != "" {
			return m.selectID(s.ParentID)
		}
	case "right", "l":
		if ok && m.collapsed[s.ID] {
			delete(m.collapsed, s.ID)
			m.rebuild()
		}
	case " ":
		if ok && (m.collapsed[s.ID] || len(m.rows[m.cursor].node.Children) > 0) {
			m.collapsed[s.ID] = !m.collapsed[s.ID]
			m.rebuild()
		}
	case "enter":
		if ok {
			return m, m.jump(s)
		}
	case "r":
		if ok {
			if s.Live() {
				return m, m.jump(s)
			}
			return m, m.resume(s, "")
		}
	case "s":
		if ok {
			m.mode = modeSend
			m.input.Reset()
			m.sendLabel = "send to " + label(s) + " › "
			m.layoutSend()
			return m, m.input.Focus()
		}
	case "n":
		return m.openForm(store.Session{})
	case "c":
		if ok && !s.Synthetic() {
			return m.openForm(s)
		}
		m.setFlash("pick a session to start a child of", true)
	case "x":
		if ok {
			if s.PID <= 0 {
				m.setFlash(label(s)+" isn't running as a process of its own", true)
				return m, nil
			}
			m.mode, m.confirm = modeConfirm, confirmation{action: actStop, session: s}
		}
	case "d":
		if ok {
			family := under(m.sessions, s.ID)
			switch {
			case s.Synthetic():
				m.setFlash(label(s)+" hasn't reported in yet; hive can't delete it", true)
			case s.Live() || slices.ContainsFunc(family, store.Session.Live):
				m.setFlash(label(s)+" or something under it is still running; stop it first", true)
			default:
				m.mode, m.confirm = modeConfirm, confirmation{action: actTrash, session: s, below: len(family)}
			}
		}
	case "t":
		return m.showTrash(true)
	case "/":
		m.mode = modeFilter
		m.filter.Width = max(10, m.width/2)
		return m, m.filter.Focus()
	case "a":
		m.showAll = !m.showAll
		m.rebuild()
		return m, m.previewCmd()
	case "i":
		m.hideSubs = !m.hideSubs
		m.rebuild()
		return m, m.previewCmd()
	case "S":
		if !m.syncing {
			m.setFlash("syncing history…", false)
			return m, m.syncCmd()
		}
	case "y":
		if ok {
			id := s.ID
			ops := m.ops
			return m, func() tea.Msg {
				return doneMsg{text: "copied " + id, err: ops.Copy(id)}
			}
		}
	case "tab":
		m.showPreview = !m.showPreview
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

// keyTrash handles the keys that differ in the trash; moving around, folding
// and filtering work as in the tree.
func (m Model) keyTrash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s, ok := m.selected()
	switch msg.String() {
	case "esc":
		if m.filter.Value() != "" {
			return m.keyNormal(msg)
		}
		return m.showTrash(false)
	case "t":
		return m.showTrash(false)
	case "r":
		if ok {
			ops := m.ops
			return m, func() tea.Msg {
				family, err := ops.Restore(s)
				return doneMsg{text: "restored " + label(s) + belowText(len(family)-1), err: err}
			}
		}
	case "d":
		if ok {
			m.mode, m.confirm = modeConfirm, confirmation{action: actPurge, session: s, below: len(under(m.trashed, s.ID))}
		}
	case "enter", "s", "n", "c", "x", "a":
		m.setFlash("in the trash: r restores, d deletes for good, t goes back", true)
	default:
		return m.keyNormal(msg)
	}
	return m, nil
}

// showTrash switches between the tree and the trash.
func (m Model) showTrash(on bool) (tea.Model, tea.Cmd) {
	m.trash = on
	m.filter.SetValue("")
	m.cursor, m.offset, m.selID = 0, 0, ""
	m.rebuild()
	return m, m.previewCmd()
}

func (m Model) keyFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.filter.Blur()
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.filter.Blur()
		m.filter.SetValue("")
		m.rebuild()
		return m, m.previewCmd()
	case "up", "down":
		m.mode = modeNormal
		m.filter.Blur()
		return m.keyNormal(msg)
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.rebuild()
	return m, tea.Batch(cmd, m.previewCmd())
}

func (m Model) keySend(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "enter":
		m.mode = modeNormal
		m.input.Blur()
		s, ok := m.selected()
		text := m.input.Value()
		if !ok || strings.TrimSpace(text) == "" {
			return m, nil
		}
		ops, name := m.ops, label(s)
		reopens := s.Pane == ""
		quit := m.popup && reopens
		return m, func() tea.Msg {
			how, err := ops.Send(s, text)
			return doneMsg{text: how + " → " + name, err: err, quit: quit && err == nil}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.clampScroll()
	return m, cmd
}

func (m Model) keyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	if msg.String() != "y" {
		return m, nil
	}
	c, ops, name := m.confirm, m.ops, label(m.confirm.session)
	switch c.action {
	case actTrash:
		return m, func() tea.Msg {
			family, err := ops.Trash(c.session)
			return doneMsg{text: "moved " + name + belowText(len(family)-1) + " to the trash; t shows it", err: err}
		}
	case actPurge:
		return m, func() tea.Msg {
			purged, err := ops.Purge(c.session)
			if err != nil {
				if n := len(purged.Sessions); n > 0 {
					err = fmt.Errorf("deleted %d for good, then stopped: %w", n, err)
				}
				return doneMsg{err: err}
			}
			text := "deleted " + name + belowText(len(purged.Sessions)-1) + " for good"
			if len(purged.Kept) > 0 {
				text += "; hive can't delete " + strings.Join(purged.Kept, ", ") + " sessions, whose own copies stay"
			}
			return doneMsg{text: text}
		}
	}
	return m, func() tea.Msg {
		return doneMsg{text: "stopped " + name, err: ops.Stop(c.session)}
	}
}

func (m Model) openForm(parent store.Session) (tea.Model, tea.Cmd) {
	tools := m.ops.Tools()
	if len(tools) == 0 {
		m.setFlash("no agent found on PATH", true)
		return m, nil
	}
	f := form{parent: parent, tools: tools, field: 1}
	f.folder = newInput("", "folder")
	f.prompt = newPromptArea("first prompt (optional)")
	f.layout(m.formWidth())
	dir := parent.Cwd
	if s, ok := m.selected(); dir == "" && ok {
		dir = s.Cwd
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	f.folder.SetValue(dir)
	f.folder.CursorEnd()
	for i, t := range tools {
		if t == parent.Tool || (parent.Tool == "" && i == 0) {
			f.tool = i
		}
	}
	m.form = f
	m.mode = modeNew
	return m, m.form.folder.Focus()
}

func (m Model) keyNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.form
	if f.field == 1 && len(f.folders) > 0 {
		if cmd, ok := f.keyFolders(msg); ok {
			return m, cmd
		}
	}
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "tab", "down":
		return m, f.focus((f.field + 1) % 3)
	case "shift+tab", "up":
		return m, f.focus((f.field + 2) % 3)
	case "left", "right":
		if f.field == 0 {
			step := 1
			if msg.String() == "left" {
				step = len(f.tools) - 1
			}
			f.tool = (f.tool + step) % len(f.tools)
			return m, nil
		}
	case "enter":
		if f.field < 2 {
			return m, f.focus(f.field + 1)
		}
		m.mode = modeNormal
		opts := tracker.LaunchOptions{
			Tool: f.tools[f.tool], Cwd: strings.TrimSpace(f.folder.Value()),
			Prompt: strings.TrimSpace(f.prompt.Value()), ParentID: f.parent.ID,
		}
		ops, quit := m.ops, m.popup
		return m, func() tea.Msg {
			_, err := ops.Launch(opts)
			return doneMsg{text: "started " + opts.Tool + " in " + shortPath(opts.Cwd), err: err, quit: quit && err == nil}
		}
	}
	var cmd tea.Cmd
	switch f.field {
	case 1:
		typed := f.folder.Value()
		f.folder, cmd = f.folder.Update(msg)
		if f.folder.Value() != typed {
			f.suggest()
		}
	case 2:
		f.prompt, cmd = f.prompt.Update(msg)
	}
	return m, cmd
}

func (f *form) focus(field int) tea.Cmd {
	f.field = field
	f.folders = nil
	f.folder.Blur()
	f.prompt.Blur()
	switch field {
	case 1:
		return f.folder.Focus()
	case 2:
		return f.prompt.Focus()
	}
	return nil
}

// jump brings s to the screen: its pane, or its parent's for a subagent;
// a session that isn't running reopens in its tool's own TUI.
func (m *Model) jump(s store.Session) tea.Cmd {
	if pane := m.paneFor(s); pane != "" {
		ops, quit := m.ops, m.popup
		return func() tea.Msg {
			err := ops.Focus(pane)
			return doneMsg{text: "jumped to " + label(s), err: err, quit: quit && err == nil}
		}
	}
	if s.Live() {
		switch {
		case s.Kind == store.KindHeadless:
			m.setFlash(label(s)+" is a headless run: its output is in the preview", false)
		case s.Kind == store.KindInternal:
			m.setFlash(label(s)+" runs inside its parent, which isn't in a tmux pane", false)
		default:
			m.setFlash(label(s)+" runs outside tmux (pid "+fmt.Sprint(s.PID)+"): hive can't show it here", true)
		}
		return nil
	}
	return m.resume(s, "")
}

func (m *Model) resume(s store.Session, prompt string) tea.Cmd {
	if err := m.ops.CanResume(s); err != nil {
		m.setFlash("can't reopen "+label(s)+": "+err.Error(), true)
		return nil
	}
	ops, quit := m.ops, m.popup
	return func() tea.Msg {
		_, err := ops.Resume(s, prompt)
		return doneMsg{text: "reopened " + label(s), err: err, quit: quit && err == nil}
	}
}

// paneFor returns the pane showing s: its own, or the nearest ancestor's
// for a subagent living inside its parent's process.
func (m *Model) paneFor(s store.Session) string {
	for range 32 {
		if s.Pane != "" {
			return s.Pane
		}
		if s.Kind != store.KindInternal {
			return ""
		}
		parent, ok := m.byID[s.ParentID]
		if !ok {
			return ""
		}
		s = parent
	}
	return ""
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.move(-3)
	case tea.MouseButtonWheelDown:
		return m.move(3)
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress || msg.X >= m.treeWidth() {
			return m, nil
		}
		if i := m.offset + msg.Y - 1; msg.Y >= 1 && i < len(m.rows) && msg.Y <= m.bodyHeight() {
			if i == m.cursor && !m.trash {
				return m, m.jump(m.rows[i].node.Session)
			}
			return m.move(i - m.cursor)
		}
	}
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	if len(m.rows) == 0 {
		return m, nil
	}
	m.cursor = max(0, min(len(m.rows)-1, m.cursor+delta))
	m.selID = m.rows[m.cursor].node.ID
	m.clampScroll()
	return m, m.previewCmd()
}

func (m Model) selectID(id string) (tea.Model, tea.Cmd) {
	for i, r := range m.rows {
		if r.node.ID == id {
			return m.move(i - m.cursor)
		}
	}
	return m, nil
}

func (m Model) selected() (store.Session, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return store.Session{}, false
	}
	return m.rows[m.cursor].node.Session, true
}

// rebuild lays the tree, or the trash, out as rows, keeping the selection on
// the same session. The trash shows everything in it, empty sessions too:
// they go with whatever they are under.
func (m *Model) rebuild() {
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var roots []*tree.Node
	switch {
	case m.trash:
		roots = tree.Build(m.trashed)
	case !m.showAll && query == "":
		roots = tree.Recent(tree.DropEmpty(tree.Build(m.sessions)), m.now().Add(-recentWindow).UnixMilli())
	default:
		roots = tree.DropEmpty(tree.Build(m.sessions))
	}
	if m.hideSubs {
		roots = tree.Prune(roots, func(n *tree.Node) bool { return n.Kind == store.KindInternal })
	}
	if query != "" {
		roots = tree.Filter(roots, func(n *tree.Node) bool { return matches(n.Session, query) })
	}
	m.rows = m.rows[:0]
	var walk func(nodes []*tree.Node, indent string, root bool)
	walk = func(nodes []*tree.Node, indent string, root bool) {
		for i, n := range nodes {
			prefix, next := "", ""
			if !root {
				if i == len(nodes)-1 {
					prefix, next = indent+"└─ ", indent+"   "
				} else {
					prefix, next = indent+"├─ ", indent+"│  "
				}
			}
			r := row{node: n, prefix: prefix}
			if m.collapsed[n.ID] && query == "" {
				r.hidden = count(n.Children)
				m.rows = append(m.rows, r)
				continue
			}
			m.rows = append(m.rows, r)
			walk(n.Children, next, false)
		}
	}
	walk(roots, "", true)

	m.cursor = min(m.cursor, max(0, len(m.rows)-1))
	for i, r := range m.rows {
		if r.node.ID == m.selID {
			m.cursor = i
			break
		}
	}
	if len(m.rows) > 0 {
		if m.selID != m.rows[m.cursor].node.ID {
			m.preview = preview{}
		}
		m.selID = m.rows[m.cursor].node.ID
	}
	m.clampScroll()
}

func count(nodes []*tree.Node) int {
	n := len(nodes)
	for _, c := range nodes {
		n += count(c.Children)
	}
	return n
}

func matches(s store.Session, query string) bool {
	for _, field := range []string{title(s), s.Title, s.LastPrompt, s.Cwd, s.ID, s.Tool} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func (m *Model) clampScroll() {
	h := m.bodyHeight()
	if h <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.rows)-h)))
}

// label names a session in messages.
func label(s store.Session) string {
	t := title(s)
	if r := []rune(t); len(r) > 40 {
		t = string(r[:39]) + "…"
	}
	return s.Tool + " ‹" + t + "›"
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
	case "pending":
		return "(new session: waiting for its first message)"
	case store.SourceLaunch:
		return "(new session)"
	}
	return "(untitled)"
}
