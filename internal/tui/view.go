package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/human"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func (m Model) bodyHeight() int { return max(1, m.height-3-max(0, m.sendRows()-1)) }

func (m Model) previewVisible() bool { return m.showPreview && m.width >= 100 }

func (m Model) usageTakesTheBody() bool {
	return m.showUsage && m.showPreview && !m.previewVisible() && m.mode != modeNew && m.mode != modeHelp
}

func (m Model) treeWidth() int {
	if !m.previewVisible() {
		return m.width
	}
	return max(44, min(110, m.width*52/100))
}

func (m Model) formWidth() int {
	if !m.previewVisible() {
		return m.width
	}
	return m.width - m.treeWidth() - 3
}

// View draws the whole screen: header, tree and preview, key hints, status.
func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	h := m.bodyHeight()
	var body []string
	switch {
	case m.mode == modeHelp:
		body = fit(helpLines(), m.width, h)
	case m.usageTakesTheBody():
		body = m.previewLines(m.width, h)
	case m.previewVisible():
		tw := m.treeWidth()
		pw := m.width - tw - 3
		left := m.treeLines(tw, h)
		var right []string
		if m.mode == modeNew {
			right = m.formLines(pw, h)
		} else {
			right = m.previewLines(pw, h)
		}
		sep := sRule.Render(" │ ")
		for i := range h {
			body = append(body, pad(left[i], tw)+sep+right[i])
		}
	case m.mode == modeNew:
		body = m.formLines(m.width, h)
	default:
		body = m.treeLines(m.width, h)
	}
	lines := append([]string{m.headerLine()}, body...)
	lines = append(lines, m.hintLines()...)
	lines = append(lines, m.statusLine())
	return strings.Join(lines, "\n")
}

func (m Model) headerLine() string {
	live, total := 0, 0
	for _, s := range m.sessions {
		total++
		if s.Live() {
			live++
		}
	}
	left := sBadge.Render("hive") + "  " +
		lipgloss.NewStyle().Foreground(cIdle).Render("●") + sText.Render(fmt.Sprintf(" %d live", live))
	if n := len(tracker.NeedsYou(m.sessions)); n > 0 {
		left += sDim.Render("  ·  ") + lipgloss.NewStyle().Foreground(cAttention).Bold(true).Render(fmt.Sprintf("◆ %d need you", n))
	}
	left += sDim.Render(fmt.Sprintf("  ·  %d sessions", total))
	if len(m.trashed) > 0 {
		left += sDim.Render(fmt.Sprintf("  ·  %d in the trash", len(m.trashed)))
	}
	scope := "last 24h"
	switch {
	case m.trash:
		left = sBadge.Render("hive") + "  " + sText.Render("trash") +
			sDim.Render(fmt.Sprintf("  ·  %d sessions, deleted from hive; their tools still have them", len(m.trashed)))
		scope = "t goes back"
	case m.showAll:
		scope = "all history"
	}
	if q := m.filter.Value(); q != "" {
		scope = "matching “" + q + "”"
	}
	if m.hideSubs {
		scope += " · subagents hidden"
	}
	if m.syncing {
		scope = "syncing… · " + scope
	}
	right := sDim.Render(scope)
	if total, estimate := m.costInView(); total > 0 {
		mark := ""
		if estimate {
			mark = "~"
		}
		right += sDim.Render(" · ") + sText.Render(mark+human.USD(total))
	}
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) treeLines(w, h int) []string {
	var out []string
	switch {
	case !m.loaded:
		out = []string{sDim.Render(" loading sessions…")}
	case len(m.rows) == 0 && m.filter.Value() != "":
		out = []string{sDim.Render(" nothing matches; esc clears the filter")}
	case len(m.rows) == 0 && m.trash:
		out = []string{sDim.Render(" the trash is empty"), "", sDim.Render(" t  back to the tree")}
	case len(m.rows) == 0:
		out = []string{sDim.Render(" nothing active in the last 24h"), "",
			sDim.Render(" a  show all history"), sDim.Render(" n  start an agent")}
	}
	for i := m.offset; i < len(m.rows) && len(out) < h; i++ {
		out = append(out, m.rowLine(m.rows[i], w, i == m.cursor))
	}
	return fit(out, w, h)
}

// rowLine draws one session: tree lines, fold mark, status, tool, title,
// kind and age. The selected row is highlighted across its whole width.
func (m Model) rowLine(r row, w int, sel bool) string {
	s := r.node.Session
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
	case len(r.node.Children) > 0:
		fold = "▾ "
	}
	g, gs := glyph(s)
	tag := map[string]string{store.KindHeadless: "run", store.KindInternal: "sub"}[s.Kind]
	when := age(m.now(), s.UpdatedAt)

	lead := ansi.StringWidth(r.prefix) + 2 + 1 + 1 + 8 + 1
	tail := 1 + 3 + 1 + 4
	tw := w - lead - tail
	t := title(s)
	if r.hidden > 0 {
		t += fmt.Sprintf(" (+%d)", r.hidden)
	}
	titleStyle := sText
	if !s.Live() {
		titleStyle = sDim
	}
	if sel {
		titleStyle = titleStyle.Bold(true)
	}
	sp := on(lipgloss.NewStyle()).Render(" ")
	line := on(sDim).Render(r.prefix+fold) + on(gs).Render(g) + sp +
		on(toolStyle(s.Tool)).Render(fmt.Sprintf("%-8s", ansi.Truncate(s.Tool, 8, ""))) + sp
	if tw >= 8 {
		line += on(titleStyle).Render(pad(ansi.Truncate(t, tw, "…"), tw)) + sp +
			on(sDim).Render(fmt.Sprintf("%-3s", tag)) + sp + on(sDim).Render(fmt.Sprintf("%4s", when))
	} else {
		line += on(titleStyle).Render(ansi.Truncate(t, max(1, w-lead), "…"))
	}
	if gap := w - ansi.StringWidth(line); gap > 0 {
		line += on(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(line, w, "")
}

// previewLines shows who the selected session is and what it is doing.
func (m Model) previewLines(w, h int) []string {
	s, ok := m.selected()
	if !ok {
		return fit(nil, w, h)
	}
	g, gs := glyph(s)
	head := []string{
		toolStyle(s.Tool).Bold(true).Render(s.Tool) + " " + sBold.Render(ansi.Truncate(title(s), w-len(s.Tool)-1, "…")),
		gs.Render(g+" "+statusWord(s)) + sDim.Render(m.where(s)),
	}
	if s.Cwd != "" {
		head = append(head, sDim.Render(ansi.Truncate(shortPath(s.Cwd), w, "…")))
	}
	if s.ParentID != "" {
		from := s.ParentID
		if p, ok := m.byID[s.ParentID]; ok {
			from = label(p)
		}
		head = append(head, sDim.Render(ansi.Truncate("from "+from, w, "…")))
	}
	if s.DeletedAt > 0 {
		head = append(head, sError.Render(ansi.Truncate("in the trash since "+age(m.now(), s.DeletedAt)+
			" ago · r restores it · d deletes it for good", w, "…")))
	}
	ids := s.ID
	if s.CreatedAt > 0 {
		ids += " · started " + age(m.now(), s.CreatedAt) + " ago"
	}
	head = append(head, sDim.Render(ansi.Truncate(ids, w, "…")))
	if !m.showUsage {
		head = append(head, m.usageSummary(s, w, summaryRows(h))...)
	}
	head = append(head, sRule.Render(strings.Repeat("─", w)))
	room := max(0, h-len(head))
	if m.showUsage {
		return fit(append(head, m.usageLines(s, m.selectedNode(), w, room)...), w, h)
	}
	return fit(append(head, m.previewContent(s, w, room)...), w, h)
}

// where says where a session runs and what Enter does with it.
func (m Model) where(s store.Session) string {
	var parts []string
	if s.Kind != "" {
		parts = append(parts, s.Kind)
	}
	switch pane := m.paneFor(s); {
	case pane != "" && pane == s.Pane:
		parts = append(parts, "pane "+pane, "↵ jumps there")
	case pane != "":
		parts = append(parts, "inside its parent", "↵ jumps to the parent")
	case s.PID > 0:
		parts = append(parts, fmt.Sprintf("pid %d", s.PID), "not in tmux")
	case s.DeletedAt == 0 && m.ops.CanResume(s) == nil:
		parts = append(parts, "↵ reopens it")
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

func (m Model) previewContent(s store.Session, w, n int) []string {
	if n <= 0 {
		return nil
	}
	p := m.preview
	var lines []string
	switch {
	case p.id != s.ID:
		lines = []string{sDim.Render("…")}
	case p.screen != nil:
		screen := p.screen
		for len(screen) > 0 && strings.TrimSpace(ansi.Strip(screen[len(screen)-1])) == "" {
			screen = screen[:len(screen)-1]
		}
		for _, l := range screen {
			lines = append(lines, ansi.Truncate(l, w, "")+"\x1b[0m")
		}
	case len(p.entries) > 0:
		for _, e := range p.entries {
			switch e.Role {
			case "user":
				lines = append(lines, "")
				for i, l := range strings.Split(ansi.Wrap(e.Text, max(10, w-2), ""), "\n") {
					mark := "  "
					if i == 0 {
						mark = "› "
					}
					lines = append(lines, sAccent.Render(mark)+sBold.Render(l))
				}
			case "tool":
				lines = append(lines, sDim.Render(ansi.Truncate("  ⚙ "+oneLine.Replace(e.Text), w, "…")))
			default:
				for _, l := range strings.Split(ansi.Wrap(e.Text, max(10, w), ""), "\n") {
					lines = append(lines, sText.Render(l))
				}
			}
		}
	case p.err != "":
		lines = []string{sDim.Render(ansi.Wrap(p.err, w, ""))}
	default:
		lines = []string{sDim.Render("nothing to show yet")}
	}
	return lines[max(0, len(lines)-n):]
}

func (m Model) formLines(w, h int) []string {
	f := m.form
	var out []string
	heading := "New agent"
	if f.parent.ID != "" {
		heading += sDim.Render("  child of " + label(f.parent))
	}
	out = append(out, sBold.Render(heading), "")
	var tools []string
	for i, t := range f.tools {
		if i == f.tool {
			tools = append(tools, toolStyle(t).Bold(true).Render("‹ "+t+" ›"))
		} else {
			tools = append(tools, sDim.Render(t))
		}
	}
	field := func(i int, name, value string) string {
		mark := "  "
		if f.field == i {
			mark = sAccent.Render("› ")
		}
		return mark + sDim.Render(fmt.Sprintf("%-7s", name)) + value
	}
	folders := f.folderLines(h - formChromeRows - 1)
	prompt := visibleRows(f.prompt, h-formChromeRows-len(folders))
	out = append(out,
		field(0, "tool", strings.Join(tools, "  ")),
		field(1, "folder", f.folder.View()),
	)
	out = append(out, folders...)
	out = append(out, field(2, "prompt", prompt[0]))
	for _, line := range prompt[1:] {
		out = append(out, strings.Repeat(" ", 9)+line)
	}
	out = append(out, "", sDim.Render("It opens in a new tmux window, running the tool's own TUI."))
	if f.parent.ID != "" {
		out = append(out, sDim.Render("hive links it under the selected session."))
	}
	return fit(out, w, h)
}

// folderLines draws the folder suggestions under the folder field in at most
// rows lines, keeping the highlighted one in view.
func (f form) folderLines(rows int) []string {
	n := len(f.folders)
	if f.field != 1 {
		return nil
	}
	shown := min(n, shownFolders, rows)
	if shown < n {
		shown = min(shown, rows-1) // a line for the count
	}
	if shown < 1 {
		return nil
	}
	start := min(max(0, f.pick-shown+1), n-shown)
	var out []string
	for i := start; i < start+shown; i++ {
		name := filepath.Base(f.folders[i]) + "/"
		if i == f.pick {
			out = append(out, strings.Repeat(" ", 7)+sAccent.Render("› "+name))
		} else {
			out = append(out, strings.Repeat(" ", 9)+sText.Render(name))
		}
	}
	if shown < n {
		out = append(out, strings.Repeat(" ", 9)+sDim.Render(fmt.Sprintf("%d–%d of %d", start+1, start+shown, n)))
	}
	return out
}

func (m Model) hintLines() []string {
	if m.mode != modeSend {
		return []string{ansi.Truncate(m.hintLine(), m.width, "…")}
	}
	var out []string
	for _, line := range visibleRows(m.input, m.sendRows()) {
		out = append(out, ansi.Truncate(line, m.width, ""))
	}
	return out
}

func (m Model) hintLine() string {
	switch m.mode {
	case modeFilter:
		return m.filter.View()
	case modeConfirm:
		c := m.confirm
		question := fmt.Sprintf("stop %s (pid %d)? ", label(c.session), c.session.PID)
		switch c.action {
		case actTrash:
			question = fmt.Sprintf("move %s%s to the trash? ", label(c.session), belowText(c.below))
		case actPurge:
			question = fmt.Sprintf("delete %s%s for good, transcripts and all? there's no undo ",
				label(c.session), belowText(c.below))
		}
		return sError.Render(question) + keys("y", "yes", "any other key", "no")
	case modeNew:
		if f := m.form; f.field == 1 && len(f.folders) > 0 {
			return keys("tab", "fill in", "↑/↓", "pick", "↵", "next", "esc", "hide list")
		}
		return keys("↵", "next / start", "tab", "field", "←/→", "tool", "esc", "cancel")
	case modeHelp:
		return keys("any key", "close")
	}
	if m.trash {
		return ansi.Truncate(keys("r", "restore", "d", "delete for good", "t", "back", "u", "usage", "/", "filter",
			"i", "subagents", "?", "help", "q", "quit"), m.width, "…")
	}
	return ansi.Truncate(keys("↵", "jump", "s", "send", "n", "new", "c", "child", "r", "reopen", "x", "stop",
		"d", "delete", "t", "trash", "u", "usage", "/", "filter", "a", "all", "i", "subagents", "?", "help", "q", "quit"), m.width, "…")
}

func keys(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, "  ")
}

func (m Model) statusLine() string {
	if m.flash == "" {
		return ""
	}
	if m.flashErr {
		return ansi.Truncate(sError.Render("✗ "+m.flash), m.width, "…")
	}
	return ansi.Truncate(sOK.Render("✓ ")+sText.Render(m.flash), m.width, "…")
}

func helpLines() []string {
	rows := [][2]string{
		{"↵", "jump to the session's pane; reopen it if it isn't running"},
		{"s", "send a message: typed into its pane, or reopens it with the message"},
		{"n / c", "start a new agent / a new agent as a child of the selected one"},
		{"r", "reopen a finished session in its tool's own TUI"},
		{"x", "stop the session's process (asks first)"},
		{"d", "move the session and everything under it to the trash (asks first)"},
		{"t", "show the trash: r restores, d deletes for good, from its tool too (asks first)"},
		{"/", "filter by title, folder or ID (searches all history)"},
		{"a", "show all history / only the last 24h"},
		{"i", "hide / show subagents"},
		{"←/→ space", "fold and unfold; ← on a leaf goes to its parent"},
		{"y", "copy the session ID"},
		{"S", "sync history now (it also runs every 30s)"},
		{"tab", "hide / show the preview"},
		{"u", "show / hide what the session used: model, tokens, price, context, tools, skills"},
		{"q", "quit (esc too, in the popup)"},
	}
	out := []string{sBold.Render("hive keys"), ""}
	for _, r := range rows {
		out = append(out, "  "+sKey.Render(fmt.Sprintf("%-10s", r[0]))+sText.Render(r[1]))
	}
	out = append(out, "",
		"  "+sDim.Render("● working  ◆ needs you  ◉ idle  ◌ running / untracked  ○ exited"),
		"  "+sDim.Render("run: a headless run   sub: a tool's own subagent"),
		"  "+sDim.Render("$ in the header: what the sessions in view cost · ~ an estimate from list prices · partial: totals come when the session ends"))
	return out
}

// oneLine makes s safe to draw on a single line.
var oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", "  ")

// fit pads or cuts lines to exactly h lines of width w.
func fit(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := range h {
		if i < len(lines) {
			out[i] = pad(ansi.Truncate(oneLine.Replace(lines[i]), w, ""), w)
		} else {
			out[i] = strings.Repeat(" ", max(0, w))
		}
	}
	return out
}

func pad(s string, w int) string {
	if gap := w - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
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
	case d < 100*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return time.UnixMilli(ms).Format("Jan")
}

func shortPath(p string) string {
	if home := paths.Home(); strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
