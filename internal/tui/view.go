package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/human"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

func (m Model) bodyHeight() int {
	hints := 1
	switch m.mode {
	case modeSend:
		hints = m.sendRows()
	case modeChannel:
		hints = m.channelRows()
	}
	return max(1, m.height-2-hints)
}

// previewMinWidth is the narrowest window that shows the preview beside the tree.
const previewMinWidth = 100

func (m Model) previewVisible() bool { return m.showPreview && m.width >= previewMinWidth }

// previewNarrow says the preview is on but the window is too narrow to show it.
func (m Model) previewNarrow() bool { return m.showPreview && m.width < previewMinWidth }

func (m Model) usageTakesTheBody() bool {
	return m.showUsage && m.showPreview && !m.previewVisible() &&
		m.mode != modeNew && m.mode != modeHelp && m.mode != modeChannel && m.mode != modeTasks &&
		m.mode != modeTeam
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
		lines := m.helpLines(m.width)
		top := min(max(0, m.helpScroll), max(0, len(lines)-h))
		body = fit(lines[top:], m.width, h)
	case m.mode == modeGoal:
		lines := m.goalLines(m.width)
		top := min(max(0, m.goalScroll), max(0, len(lines)-h))
		body = fit(lines[top:], m.width, h)
	case m.mode == modeChannel:
		body = m.channelLines(m.width, h)
	case m.mode == modeTasks:
		body = m.taskEditorLines(m.width, h)
	case m.usageTakesTheBody():
		body = m.previewLines(m.width, h)
	case m.previewVisible():
		tw := m.treeWidth()
		pw := m.width - tw - 3
		left := m.treeLines(tw, h)
		var right []string
		switch m.mode {
		case modeNew:
			right = m.formLines(pw, h)
		case modeTeam:
			right = m.teamFormLines(pw, h)
		default:
			right = m.previewLines(pw, h)
		}
		sep := sRule.Render(" │ ")
		for i := range h {
			body = append(body, pad(left[i], tw)+sep+right[i])
		}
	case m.mode == modeNew:
		body = m.formLines(m.width, h)
	case m.mode == modeTeam:
		body = m.teamFormLines(m.width, h)
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
	segs := []string{sBadge.Render("hive") + "  " +
		lipgloss.NewStyle().Foreground(cIdle).Render("●") + sText.Render(fmt.Sprintf(" %d live", live))}
	if n := len(tracker.NeedsYou(m.sessions)); n > 0 {
		segs = append(segs, lipgloss.NewStyle().Foreground(cAttention).Bold(true).Render(fmt.Sprintf("◆ %d need you", n)))
	}
	if len(m.notConnected) > 0 {
		segs = append(segs, sError.Render("⚠ not connected: "+strings.Join(m.notConnected, ", ")+" · run hive install"))
	}
	if n := m.teamCount(); n == 1 {
		segs = append(segs, sAccent.Render("1 team"))
	} else if n > 1 {
		segs = append(segs, sAccent.Render(fmt.Sprintf("%d teams", n)))
	}
	segs = append(segs, sDim.Render(fmt.Sprintf("%d sessions", total)))
	if len(m.snap.Trashed) > 0 {
		segs = append(segs, sDim.Render(m.trashCount()+" in the trash"))
	} else if len(m.trashed) > 0 {
		segs = append(segs, sDim.Render(fmt.Sprintf("%d in the trash", len(m.trashed))))
	}
	scope := "last 24h"
	switch {
	case m.trash:
		segs = []string{sBadge.Render("hive") + "  " + sText.Render("trash") +
			sDim.Render("  ·  "+m.trashCount()+", deleted from hive; their tools still have them")}
		scope = "t goes back"
	case m.showAll:
		scope = "all history"
	}
	if q := m.filter.Value(); q != "" {
		if ts, ok := teamFilter(strings.ToLower(strings.TrimSpace(q))); ok {
			if ts == "" {
				scope = "all teams"
			} else {
				scope = "team “" + ts + "”"
			}
		} else {
			scope = "matching “" + strings.TrimSpace(q) + "” · all history"
		}
	}
	if m.hideSubs {
		scope += " · subagents hidden"
	}
	if m.syncing {
		scope = "syncing… · " + scope
	}
	right := []string{sDim.Render(scope)}
	if m.previewNarrow() {
		right = append(right, sDim.Render(fmt.Sprintf("preview needs %d cols", previewMinWidth)))
	} else if cost, estimate := m.costInView(); cost > 0 {
		mark := ""
		if estimate {
			mark = "~"
		}
		right = append(right, sText.Render(mark+human.USD(cost)))
	}
	sep, rightSep := sDim.Render("  ·  "), sDim.Render(" · ")
	// Drop whole segments from the end, the least important first, so the
	// header never cuts one in half. The right side goes last, which keeps the
	// preview note while the left still has something to give.
	for {
		left, tail := strings.Join(segs, sep), strings.Join(right, rightSep)
		gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(tail)
		switch {
		case gap >= 1:
			return left + strings.Repeat(" ", gap) + tail
		case len(segs) > 1:
			segs = segs[:len(segs)-1]
		case len(right) > 1:
			right = right[:len(right)-1]
		default:
			return ansi.Truncate(left, m.width, "")
		}
	}
}

func (m Model) treeLines(w, h int) []string {
	var out []string
	switch {
	case !m.loaded:
		out = []string{sDim.Render(" loading sessions…")}
	case len(m.rows) == 0 && m.filter.Value() != "":
		q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
		out = []string{sDim.Render(fmt.Sprintf(" nothing matches “%s” in titles, folders or IDs (all history) · esc clears", q))}
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
	if r.node.Team != nil {
		return m.teamRowLine(r, w, sel)
	}
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
	if r.node.Role != "" {
		t = r.node.Role + " · " + m.memberTitle(r.node)
	}
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
	markStyle := sAccent
	tagStyle := sDim
	if sel {
		markStyle = markStyle.Background(cSelect)
		tagStyle = tagStyle.Background(cSelect)
	}
	sp := on(lipgloss.NewStyle()).Render(" ")
	line := on(sDim).Render(r.prefix+fold) + on(gs).Render(g) + sp +
		on(toolStyle(s.Tool)).Render(fmt.Sprintf("%-8s", ansi.Truncate(s.Tool, 8, ""))) + sp
	if tw >= 8 {
		shown := m.reasonTitle(s, t, tw, titleStyle, markStyle, tagStyle)
		line += shown + on(lipgloss.NewStyle()).Render(strings.Repeat(" ", max(0, tw-ansi.StringWidth(shown)))) + sp +
			on(sDim).Render(fmt.Sprintf("%-3s", tag)) + sp + on(sDim).Render(fmt.Sprintf("%4s", when))
	} else {
		line += m.reasonTitle(s, t, max(1, w-lead), titleStyle, markStyle, tagStyle)
	}
	if gap := w - ansi.StringWidth(line); gap > 0 {
		line += on(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(line, w, "")
}

// reasonTitle draws a row's title and says why the filter matched it: the
// matched part of a title in accent, or a dim folder or id tag at the end.
func (m Model) reasonTitle(s store.Session, t string, width int, titleStyle, markStyle, tagStyle lipgloss.Style) string {
	query, plain := m.filterMatch()
	field := ""
	if plain {
		field = matches(s, query)
	}
	tag := ""
	switch field {
	case "title":
		shown := ansi.Truncate(t, max(1, width), "…")
		return highlightMatch(shown, query, titleStyle, markStyle)
	case "folder", "id":
		tag = " " + field
	}
	shown := ansi.Truncate(t, max(1, width-ansi.StringWidth(tag)), "…")
	return titleStyle.Render(shown) + tagStyle.Render(tag)
}

// highlightMatch draws the first case-insensitive match of query in text in
// mark, the rest in base.
func highlightMatch(text, query string, base, mark lipgloss.Style) string {
	at := strings.Index(strings.ToLower(text), query)
	if query == "" || at < 0 || at+len(query) > len(text) {
		return base.Render(text)
	}
	return base.Render(text[:at]) + mark.Render(text[at:at+len(query)]) + base.Render(text[at+len(query):])
}

// previewLines shows who the selected session is and what it is doing.
func (m Model) previewLines(w, h int) []string {
	s, ok := m.selected()
	if !ok {
		return fit(nil, w, h)
	}
	if n := m.selectedNode(); n.Team != nil {
		return m.teamPreviewLines(n, w, h)
	}
	g, gs := glyph(s)
	head := []string{
		toolStyle(s.Tool).Bold(true).Render(s.Tool) + " " + sBold.Render(ansi.Truncate(m.nodeTitle(), w-len(s.Tool)-1, "…")),
		gs.Render(g+" "+statusWord(s)) + sDim.Render(m.where(s)),
	}
	if s.Cwd != "" {
		head = append(head, sDim.Render(ansi.Truncate(shortPath(s.Cwd), w, "…")))
	}
	if n := m.selectedNode(); n.Role != "" {
		if t, ok := m.teamByID(n.TeamID); ok {
			head = append(head, sAccent.Render(ansi.Truncate(n.Role+" on team "+t.Name, w, "…")))
		}
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
		if s.Source == "scan" && strings.Contains(p.err, "no transcript recorded") {
			lines = []string{sDim.Render(ansi.Wrap(fmt.Sprintf(
				"hive can't see what this does until its tool reports: hive install %s, then restart it", s.Tool), w, ""))}
		} else {
			lines = []string{sDim.Render(ansi.Wrap(p.err, w, ""))}
		}
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
		field(fieldTool, "tool", choices(f.tools, f.tool, max(8, w-9), func(t string) lipgloss.Style { return toolStyle(t) })),
		field(fieldModel, "model", f.modelLine(max(8, w-9))),
		field(fieldFolder, "folder", f.folder.View()),
	)
	out = append(out, folders...)
	out = append(out, field(fieldPrompt, "prompt", prompt[0]))
	for _, line := range prompt[1:] {
		out = append(out, strings.Repeat(" ", 9)+line)
	}
	out = append(out, "", sDim.Render(f.explain()))
	if f.parent.ID != "" {
		out = append(out, sDim.Render("hive links it under the selected session."))
	}
	return fit(out, w, h)
}

// choices draws a row of choices with the selected one in brackets. When the
// row is wider than w it shows a window around the selected choice, with … on
// either side of the hidden ones and the position at the end.
func choices(items []string, selected, w int, style func(string) lipgloss.Style) string {
	if len(items) == 0 {
		return ""
	}
	selected = max(0, min(selected, len(items)-1))
	rendered := make([]string, len(items))
	for i, item := range items {
		if i == selected {
			rendered[i] = style(item).Bold(true).Render("‹ " + item + " ›")
		} else {
			rendered[i] = sDim.Render(item)
		}
	}
	all := strings.Join(rendered, "  ")
	if ansi.StringWidth(all) <= w {
		return all
	}
	pos := sDim.Render(fmt.Sprintf("%d/%d", selected+1, len(items)))
	line := func(lo, hi int) string {
		out := strings.Join(rendered[lo:hi+1], "  ")
		if lo > 0 {
			out = sDim.Render("…") + "  " + out
		}
		if hi < len(items)-1 {
			out += "  " + sDim.Render("…")
		}
		return out + "  " + pos
	}
	lo, hi := selected, selected
	if ansi.StringWidth(line(lo, hi)) > w {
		label := ansi.Truncate(items[selected], max(1, w-8), "…")
		return ansi.Truncate(style(label).Bold(true).Render("‹ "+label+" ›"), w, "")
	}
	for {
		grew := false
		preferLeft := selected-lo <= hi-selected
		for _, left := range []bool{preferLeft, !preferLeft} {
			if left && lo > 0 {
				if next := line(lo-1, hi); ansi.StringWidth(next) <= w {
					lo, grew = lo-1, true
					break
				}
			}
			if !left && hi < len(items)-1 {
				if next := line(lo, hi+1); ansi.StringWidth(next) <= w {
					hi, grew = hi+1, true
					break
				}
			}
		}
		if !grew {
			break
		}
	}
	return line(lo, hi)
}

// modelLine draws the model field: for a tool, its choices; for auto, the
// mode, which ←/→ flips.
func (f form) modelLine(w int) string {
	if f.toolName() == fusion.Auto {
		what := "picks the tool and the model for the prompt"
		if f.mode == fusion.ModeModel {
			what = "picks only the model, inside " + f.inside
		}
		return sAccent.Bold(true).Render("‹ "+string(f.mode)+" ›") + "  " + sDim.Render(what+" · ←/→ changes the mode")
	}
	loading := ""
	if f.loadingModels() {
		loading = "  " + sDim.Render("loading what it runs…")
		w -= ansi.StringWidth("  loading what it runs…")
	}
	return choices(f.modelChoices(), min(f.model, len(f.modelChoices())-1), max(8, w), func(string) lipgloss.Style { return sAccent }) + loading
}

// explain says what starts, under the form.
func (f form) explain() string {
	switch {
	case f.toolName() == fusion.Auto:
		return "hive picks for the prompt, then opens the tool's own TUI in a new tmux window."
	case f.selectedModel() == fusion.Auto:
		return "hive picks " + f.toolName() + "'s model for the prompt and opens its TUI in a new tmux window."
	}
	return "It opens in a new tmux window, running the tool's own TUI."
}

// folderLines draws the folder suggestions under the folder field in at most
// rows lines, keeping the highlighted one in view.
func (f form) folderLines(rows int) []string {
	n := len(f.folders)
	if f.field != fieldFolder {
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
	if m.mode == modeChannel {
		var out []string
		for _, line := range visibleRows(m.channelInput, m.channelRows()) {
			out = append(out, ansi.Truncate(line, m.width, ""))
		}
		return out
	}
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
		case actStopTeam:
			question = fmt.Sprintf("stop the %d running members of %s? ", c.below, c.team.Name)
		case actTrash:
			question = fmt.Sprintf("move %s%s to the trash? ", label(c.session), belowText(c.below))
		case actPurge:
			question = fmt.Sprintf("delete %s%s for good, transcripts and all? there's no undo ",
				label(c.session), belowText(c.below))
		case actTrashTeam:
			question = fmt.Sprintf("move team %s and its %s to the trash? ", c.team.Name, membersText(c.below))
		case actPurgeTeam:
			question = fmt.Sprintf("delete team %s and its %s for good, transcripts and all? there's no undo ",
				c.team.Name, membersText(c.below))
		}
		return sError.Render(question) + keys("y", "yes", "any other key", "no")
	case modeNew:
		f := m.form
		if f.field == fieldFolder && len(f.folders) > 0 {
			return keys("tab", "completes", "↑/↓", "pick", "↵", "next", "esc", "hide list")
		}
		switch f.field {
		case fieldTool, fieldModel:
			return keys("←/→", "choose", "tab", "field", "↵", "next / start", "esc", "cancel")
		case fieldFolder:
			return keys("tab", "completes", "↵", "next", "esc", "cancel")
		default:
			return keys("↵", "starts", "tab", "field", "esc", "cancel")
		}
	case modeTeam:
		return keys("↵", "next / start", "tab", "field", "+", "row", "ctrl+d", "remove", "←/→", "work · lead · template · model", "esc", "cancel")
	case modeTasks:
		if f := m.taskEdit.form; f != nil {
			if f.kind == taskFormDone || f.kind == taskFormBlock {
				return keys("↵", "confirm", "esc", "cancel")
			}
			return keys("↵", "next / save", "tab", "field", "esc", "cancel")
		}
		return keys("j/k", "move", "n", "add", "e", "edit", "d", "done", "b", "block", "esc", "back")
	case modeGoal:
		hint := keys("↑/↓", "scroll", "esc", "back")
		total := len(m.goalLines(m.width))
		if h := m.bodyHeight(); total > h {
			first := min(m.goalScroll+1, total)
			last := min(m.goalScroll+h, total)
			hint += "  " + sDim.Render(fmt.Sprintf("· %d–%d of %d lines", first, last, total))
		}
		return hint
	case modeHelp:
		hint := keys("↑/↓", "scroll", "esc", "close")
		total := len(m.helpLines(m.width))
		if h := m.bodyHeight(); total > h {
			top := min(max(0, m.helpScroll), total-h)
			first := min(top+1, total)
			last := min(top+h, total)
			hint += "  " + sDim.Render(fmt.Sprintf("· %d–%d of %d lines", first, last, total))
		}
		return hint
	}
	if hints, ok := m.teamHints(); ok {
		return fitHints(hints, m.width, keys("?", "more"))
	}
	if m.trash {
		return fitHints([]string{
			keys("r", "restore"),
			keys("d", "delete for good"),
			keys("t", "back"),
			keys("u", "usage"),
			keys("/", "filter"),
			keys("i", "subagents"),
		}, m.width, keys("?", "more"))
	}
	return fitHints([]string{
		keys("↵", "jump"),
		keys("s", "send"),
		keys("n", "new"),
		keys("c", "child"),
		keys("u", "usage"),
		keys("/", "filter"),
		keys("r", "reopen"),
		keys("x", "stop"),
		keys("d", "trash"),
		keys("t", "show trash"),
		keys("a", "all/24h"),
		keys("i", "subagents"),
	}, m.width, keys("?", "more"))
}

// fitHints joins key hints with two spaces and drops whole hints from the end
// until the line fits w. tail is always shown, so the reader can find the
// keys that didn't fit.
func fitHints(hints []string, w int, tail string) string {
	for len(hints) > 0 {
		line := strings.Join(hints, "  ") + "  " + tail
		if ansi.StringWidth(line) <= w {
			return line
		}
		hints = hints[:len(hints)-1]
	}
	return tail
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

// helpRow is one line of the help: a key and what it does. An empty key
// makes a plain line, as the glyph legend has.
type helpRow struct {
	key, text string
	dim       bool
}

// helpSection is a titled group of help rows. The section for where the user
// is comes first; name picks it out.
type helpSection struct {
	name, title string
	rows        []helpRow
}

func treeHelp() helpSection {
	return helpSection{name: "Tree", title: "Tree", rows: []helpRow{
		{"↵", "jump to the session's pane; reopen it if it isn't running", false},
		{"s", "send a message: typed into its pane, or reopens it with the message", false},
		{"n / c", "start a new agent / a new agent as a child of the selected one; tool or model auto picks for the prompt", false},
		{"r", "reopen a finished session in its tool's own TUI", false},
		{"x", "stop the session's process (asks first)", false},
		{"d", "move the session and everything under it to the trash (asks first)", false},
		{"t", "show the trash: r restores, d deletes for good, from its tool too (asks first)", false},
		{"/", "filter by title, folder or ID (searches all history); team:<name or ID> shows a team's subtree, bare team: every team", false},
		{"a", "show all history / only the last 24h", false},
		{"i", "hide / show subagents", false},
		{"←/→ space", "fold and unfold; ← on a leaf goes to its parent", false},
		{"y", "copy the session ID", false},
		{"T", "start a team: agents that work on one goal and report to a channel", false},
		{"S", "sync history now (it also runs every 30s)", false},
		{"tab", "hide / show the preview", false},
		{"u", "show / hide what the session used: model, tokens, price, context, tools, skills", false},
		{"q", "quit (esc too, in the popup)", false},
	}}
}

func teamHelp() helpSection {
	return helpSection{name: "Team", title: "Team  (on a ⬡ row or a member)", rows: []helpRow{
		{"↵", "open the channel full screen (on a member: jump to it)", false},
		{"e", "edit the board: n add · e edit · d done · b block", false},
		{"ctrl+g", "read the whole goal and every member's part", false},
		{"s", "message everyone (@role for one); on a member, that member", false},
		{"p / L", "pause or resume · take the lead or hand it back", false},
		{"x / d", "stop every member · move the team to the trash", false},
		{"m", "hide hive's own posts", false},
	}}
}

func channelHelp() helpSection {
	return helpSection{name: "Channel", title: "Channel", rows: []helpRow{
		{"type", "write to everyone (@role for one) · ↵ sends · esc goes back", false},
		{"↑/↓", "move the caret once typing", false},
		{"pgup/pgdn", "scroll the feed", false},
		{"ctrl+g", "read the goal", false},
	}}
}

func boardHelp() helpSection {
	return helpSection{name: "Board", title: "Board  (e on a team)", rows: []helpRow{
		{"j / k", "move", false},
		{"n", "add a task", false},
		{"e", "edit the selected task", false},
		{"d", "mark it done", false},
		{"b", "mark it blocked", false},
		{"esc", "back", false},
	}}
}

func trashHelp() helpSection {
	return helpSection{name: "Trash", title: "Trash  (t)", rows: []helpRow{
		{"r", "restore a session or a team with everything under it", false},
		{"d", "delete it for good, from its tool too (asks first)", false},
		{"t", "back to the tree", false},
	}}
}

func glyphHelp() helpSection {
	return helpSection{name: "Glyphs", title: "Glyphs", rows: []helpRow{
		{"", "● working  ◆ needs you  ◉ idle  ◌ running / untracked  ○ exited", true},
		{"", "run: a headless run   sub: a tool's own subagent", true},
		{"", "$ in the header: what the sessions in view cost · ~ an estimate from list prices · partial: totals come when the session ends", true},
	}}
}

// helpSections lists the help in its fixed order.
func helpSections() []helpSection {
	return []helpSection{treeHelp(), teamHelp(), channelHelp(), boardHelp(), trashHelp(), glyphHelp()}
}

// helpWhere is the view the help was opened from; in the help itself, the
// view it will go back to.
func (m Model) helpWhere() mode {
	if m.mode == modeHelp {
		return m.helpFrom
	}
	return m.mode
}

// helpFirstSection names the section for where the user is, which the help
// shows first: the board, the trash, a team or a member row, else the tree.
func (m Model) helpFirstSection() string {
	switch {
	case m.helpWhere() == modeTasks:
		return "Board"
	case m.trash:
		return "Trash"
	}
	if n := m.selectedNode(); n != nil && (n.Team != nil || n.Role != "" || n.TeamID != "") {
		return "Team"
	}
	return "Tree"
}

// helpOrders puts the current view's section first and keeps the rest in
// their fixed order.
func (m Model) helpOrders() []helpSection {
	sections := helpSections()
	first := m.helpFirstSection()
	for i, s := range sections {
		if s.name != first {
			continue
		}
		if i > 0 {
			ordered := []helpSection{s}
			ordered = append(ordered, sections[:i]...)
			ordered = append(ordered, sections[i+1:]...)
			return ordered
		}
		break
	}
	return sections
}

// helpLines draws every section: a key column as wide as the longest key,
// descriptions wrapped under it with a hanging indent, so nothing is cut.
func (m Model) helpLines(w int) []string {
	sections := m.helpOrders()
	keyWidth := 0
	for _, s := range sections {
		for _, r := range s.rows {
			keyWidth = max(keyWidth, ansi.StringWidth(r.key))
		}
	}
	keyWidth += 2
	indent := 2 + keyWidth
	descWidth := max(1, w-indent)
	out := []string{sBold.Render("hive keys"), ""}
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, sBold.Render(s.title))
		for _, r := range s.rows {
			style := sText
			if r.dim {
				style = sDim
			}
			if r.key == "" {
				for _, line := range strings.Split(ansi.Wrap(r.text, max(1, w-2), ""), "\n") {
					out = append(out, "  "+style.Render(line))
				}
				continue
			}
			for j, line := range strings.Split(ansi.Wrap(r.text, descWidth, ""), "\n") {
				if j == 0 {
					out = append(out, "  "+sKey.Render(pad(r.key, keyWidth))+style.Render(line))
				} else {
					out = append(out, strings.Repeat(" ", indent)+style.Render(line))
				}
			}
		}
	}
	return out
}

// goalLines draws the team reader full screen: its name, how it runs, the
// whole goal with its own line breaks, and every active member's part.
func (m Model) goalLines(w int) []string {
	t, ok := m.teamByID(m.goalTeam)
	if !ok {
		return []string{sDim.Render("this team is gone; esc goes back")}
	}
	out := []string{sAccent.Bold(true).Render("⬡ " + cleanLine(t.Name))}
	if head := m.teamHead(t, w); len(head) > 1 {
		out = append(out, head[1])
	}
	out = append(out, "", sBold.Render("goal"))
	goal := team.Clean(t.Goal)
	if strings.TrimSpace(goal) == "" {
		out = append(out, indentWrapped(
			"no goal was set; the team form (T on a template, or hive team new --goal) sets one",
			w, 2, sDim)...)
	} else {
		out = append(out, indentWrapped(goal, w, 2, sText)...)
	}
	members := m.members(t.ID)
	hasParts := false
	for _, mem := range members {
		if strings.TrimSpace(mem.Prompt) != "" {
			hasParts = true
			break
		}
	}
	if hasParts {
		out = append(out, "", sBold.Render("parts"))
	}
	for _, mem := range members {
		part := strings.TrimSpace(team.Clean(mem.Prompt))
		if part == "" {
			continue
		}
		meta := mem.Tool
		if mem.Model != "" && mem.Model != fusion.Auto {
			meta += " " + mem.Model
		}
		out = append(out, "  "+toolStyle(mem.Tool).Bold(true).Render(cleanLine(mem.Role))+"  "+sDim.Render(meta))
		out = append(out, indentWrapped(part, w, 4, sText)...)
	}
	return out
}

// indentWrapped keeps text's own line breaks and wraps each line at w-indent,
// indenting it by indent spaces; a blank line stays blank.
func indentWrapped(text string, w, indent int, style lipgloss.Style) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		for _, wrapped := range strings.Split(ansi.Wrap(line, max(10, w-indent), ""), "\n") {
			out = append(out, strings.Repeat(" ", indent)+style.Render(wrapped))
		}
	}
	return out
}

// goalMaxScroll is how far the reader scrolls: enough to put its last line at
// the bottom.
func (m Model) goalMaxScroll(h int) int {
	return max(0, len(m.goalLines(m.width))-h)
}

// keyGoal scrolls the team reader; esc, q, enter and ctrl+g close it.
func (m Model) keyGoal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	h := m.bodyHeight()
	maxScroll := m.goalMaxScroll(h)
	switch msg.String() {
	case "esc", "q", "enter", "ctrl+g":
		m.mode = m.goalFrom
		if m.mode == modeChannel {
			return m, m.channelInput.Focus()
		}
		return m, nil
	case "up", "k":
		m.goalScroll = max(0, m.goalScroll-1)
	case "down", "j":
		m.goalScroll = min(maxScroll, m.goalScroll+1)
	case "pgup", "ctrl+u":
		m.goalScroll = max(0, m.goalScroll-max(1, h/2))
	case "pgdown", "ctrl+d":
		m.goalScroll = min(maxScroll, m.goalScroll+max(1, h/2))
	case "home", "g":
		m.goalScroll = 0
	case "end", "G":
		m.goalScroll = maxScroll
	}
	return m, nil
}

// helpMaxScroll is how far the help scrolls: enough to put its last line at
// the bottom.
func (m Model) helpMaxScroll(h int) int {
	return max(0, len(m.helpLines(m.width))-h)
}

// keyHelp scrolls the help; esc, q, ? and enter close it, and any other key
// leaves it where it is.
func (m Model) keyHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	h := m.bodyHeight()
	maxScroll := m.helpMaxScroll(h)
	switch msg.String() {
	case "esc", "q", "?", "enter":
		m.mode = m.helpFrom
		m.helpFrom = modeNormal
		return m, nil
	case "up", "k":
		m.helpScroll = max(0, m.helpScroll-1)
	case "down", "j":
		m.helpScroll = min(maxScroll, m.helpScroll+1)
	case "pgup", "ctrl+u":
		m.helpScroll = max(0, m.helpScroll-max(1, h/2))
	case "pgdown", "ctrl+d":
		m.helpScroll = min(maxScroll, m.helpScroll+max(1, h/2))
	case "home", "g":
		m.helpScroll = 0
	case "end", "G":
		m.helpScroll = maxScroll
	}
	return m, nil
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
