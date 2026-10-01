package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/human"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tree"
)

const (
	usageEvery   = 6 * time.Second
	usageTimeout = 10 * time.Second
	labelWidth   = 9
)

type usageFetch struct {
	id   string
	at   time.Time
	done bool
	err  string
}

func (m Model) usageFor(id string) (store.Usage, bool) {
	u, ok := m.usages[id]
	return u, ok && !u.Empty()
}

func (m Model) costInView() (float64, bool) {
	if m.trash {
		return 0, false
	}
	return tree.Cost(m.roots, m.usages)
}

func summaryRows(h int) int {
	switch {
	case h >= 18:
		return 4
	case h >= 12:
		return 2
	}
	return 1
}

func (m Model) usageSummary(s store.Session, w, rows int) []string {
	u, ok := m.usageFor(s.ID)
	if !ok || rows <= 0 {
		return nil
	}
	var candidates []string
	switch rows {
	case 1:
		candidates = []string{moneyLine(u, w)}
	case 2:
		candidates = []string{moneyLine(u, w), contextLine(u, w, 10)}
	default:
		candidates = []string{modelLine(u, w), moneyLine(u, w), contextLine(u, w, 10), toolsLine(u, w)}
	}
	var out []string
	for _, line := range candidates {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func modelLine(u store.Usage, w int) string {
	var parts []string
	if u.Model != "" {
		model := u.Model
		if n := len(u.Models); n > 1 {
			model += fmt.Sprintf(" +%d", n-1)
		}
		parts = append(parts, model)
	}
	if u.Effort != "" {
		parts = append(parts, u.Effort)
	}
	if u.Requests > 0 {
		parts = append(parts, fmt.Sprintf("%d requests", u.Requests))
	}
	if u.APIDurationMS > 0 {
		parts = append(parts, human.Span(time.Duration(u.APIDurationMS)*time.Millisecond)+" api")
	}
	return joinFit(w, styled(parts, sText)...)
}

func moneyLine(u store.Usage, w int) string {
	var parts []string
	if text, ok := costText(u); ok {
		parts = append(parts, text)
	} else if u.PremiumRequests > 0 {
		parts = append(parts, fmt.Sprintf("%g premium requests", u.PremiumRequests))
	}
	parts = append(parts, tokenParts(u.Tokens, false)...)
	line := styled(parts, sText)
	if u.Partial {
		line = append(line, sDim.Render("partial"))
	}
	return joinFit(w, line...)
}

func contextLine(u store.Usage, w, barWidth int) string {
	switch {
	case u.ContextTokens == 0:
		return ""
	case u.ContextWindow == 0:
		return sDim.Render("context ") + sText.Render(human.Tokens(u.ContextTokens))
	}
	pct := contextPercent(u)
	line := sDim.Render("context ") + sText.Render(human.Tokens(u.ContextTokens)+"/"+human.Tokens(u.ContextWindow))
	if w >= 36 {
		line += " " + bar(pct, barWidth)
	}
	return line + " " + contextStyle(pct).Render(fmt.Sprintf("%d%%", pct))
}

func contextPercent(u store.Usage) int {
	return int(math.Round(float64(u.ContextTokens) * 100 / float64(u.ContextWindow)))
}

func toolsLine(u store.Usage, w int) string {
	tools := sortedCounts(u.Tools)
	if len(tools) == 0 && len(u.Skills) == 0 {
		return ""
	}
	named := sDim.Render("skills ") + sText.Render(skillList(u.Skills))
	counted := sDim.Render(plural(len(u.Skills), "skill"))
	if len(u.Skills) == 0 {
		named, counted = "", ""
	}
	for keep := len(tools); keep >= min(2, len(tools)); keep-- {
		if line := toolParts(tools, keep, named); ansi.StringWidth(line) <= w {
			return line
		}
	}
	for keep := len(tools); keep >= 0; keep-- {
		if line := toolParts(tools, keep, counted); ansi.StringWidth(line) <= w {
			return line
		}
	}
	return ""
}

func toolParts(tools []string, keep int, skills string) string {
	parts := styled(tools[:keep], sText)
	if rest := len(tools) - keep; rest > 0 {
		parts = append(parts, sDim.Render(fmt.Sprintf("+%d", rest)))
	}
	if skills != "" {
		parts = append(parts, skills)
	}
	return strings.Join(parts, sDim.Render(" · "))
}

func skillList(skills map[string]int) string {
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func tokenParts(t store.Tokens, long bool) []string {
	var parts []string
	add := func(n int64, short, full string) {
		if n == 0 {
			return
		}
		label := short
		if long {
			label = full
		}
		parts = append(parts, human.Tokens(n)+" "+label)
	}
	add(t.Input, "in", "in")
	add(t.Output, "out", "out")
	if long {
		add(t.CacheRead, "", "cache read")
		add(t.CacheWrite, "", "cache write")
		add(t.Reasoning, "", "reasoning")
	} else {
		add(t.CacheRead+t.CacheWrite, "cache", "cache")
	}
	return parts
}

func costText(u store.Usage) (string, bool) {
	switch u.CostSource {
	case store.CostTool:
		return human.USD(u.CostUSD), true
	case store.CostConfig:
		return "~" + human.USD(u.CostUSD), true
	}
	return "", false
}

func costSourceNotes(s store.Session, u store.Usage) []string {
	switch u.CostSource {
	case store.CostTool:
		return []string{"reported by " + s.Tool}
	case store.CostConfig:
		notes := []string{"estimated from built-in or [[model]] prices"}
		if u.Partial && s.Tool == "claude" {
			notes = append(notes, "exact at exit")
		}
		return notes
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func styled(parts []string, style lipgloss.Style) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = style.Render(p)
	}
	return out
}

func joinFit(w int, parts ...string) string {
	for keep := len(parts); keep > 1; keep-- {
		if line := strings.Join(parts[:keep], sDim.Render(" · ")); ansi.StringWidth(line) <= w {
			return line
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(parts[0], w, "…")
}

func sortedCounts(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = fmt.Sprintf("%s %d", name, counts[name])
	}
	return out
}

func (m Model) usageLines(s store.Session, n *tree.Node, w, h int) []string {
	u, ok := m.usageFor(s.ID)
	switch {
	case !ok && m.usage.id == s.ID && !m.usage.done:
		return []string{sDim.Render("…")}
	case !ok && m.usage.id == s.ID && m.usage.err != "":
		return []string{sDim.Render(ansi.Wrap(m.usage.err, w, ""))}
	case !ok:
		return []string{sDim.Render("no usage recorded for this session")}
	}
	var out []string
	row := func(label string, parts ...string) {
		out = append(out, flow(label, parts, w)...)
	}
	row("model", modelParts(u)...)
	row("cost", costParts(s, u)...)
	row("tokens", tokenParts(u.Tokens, true)...)
	row("context", contextParts(u, w)...)
	row("tools", sortedCounts(u.Tools)...)
	row("skills", sortedCounts(u.Skills)...)
	if u.LinesAdded > 0 || u.LinesRemoved > 0 {
		row("lines", fmt.Sprintf("+%d −%d", u.LinesAdded, u.LinesRemoved))
	}
	if u.PremiumRequests > 0 {
		row("premium", fmt.Sprintf("%g requests", u.PremiumRequests))
	}
	if u.Partial {
		row("partial", "totals come when the session ends")
	}
	row("tree", m.treeParts(n)...)
	out = append(out, modelTable(u, w)...)
	return out
}

func modelParts(u store.Usage) []string {
	var parts []string
	if u.Model != "" {
		parts = append(parts, u.Model)
	}
	if u.Effort != "" {
		parts = append(parts, u.Effort)
	}
	if u.Requests > 0 {
		parts = append(parts, fmt.Sprintf("%d requests", u.Requests))
	}
	if u.APIDurationMS > 0 {
		parts = append(parts, human.Span(time.Duration(u.APIDurationMS)*time.Millisecond)+" api")
	}
	if u.Compactions > 0 {
		parts = append(parts, fmt.Sprintf("%d compactions", u.Compactions))
	}
	return parts
}

func costParts(s store.Session, u store.Usage) []string {
	if text, ok := costText(u); ok {
		return append([]string{text}, costSourceNotes(s, u)...)
	}
	parts := []string{"unknown"}
	if u.Model != "" {
		parts = append(parts, "no price for "+u.Model)
	}
	return parts
}

func contextParts(u store.Usage, w int) []string {
	switch {
	case u.ContextTokens == 0:
		return nil
	case u.ContextWindow == 0:
		return []string{human.Tokens(u.ContextTokens), "window unknown"}
	}
	pct := contextPercent(u)
	width := max(6, min(30, w-22))
	return []string{human.Tokens(u.ContextTokens) + "/" + human.Tokens(u.ContextWindow) + " " + bar(pct, width) + " " +
		contextStyle(pct).Render(fmt.Sprintf("%d%%", pct))}
}

func (m Model) treeParts(n *tree.Node) []string {
	if n == nil {
		return nil
	}
	var tokens store.Tokens
	below := 0
	var walk func(nodes []*tree.Node)
	walk = func(nodes []*tree.Node) {
		for _, c := range nodes {
			if u, ok := m.usageFor(c.ID); ok {
				tokens = tokens.Add(u.Tokens)
				below++
			}
			walk(c.Children)
		}
	}
	walk(n.Children)
	if below == 0 {
		return nil
	}
	if u, ok := m.usageFor(n.ID); ok {
		tokens = tokens.Add(u.Tokens)
	}
	var parts []string
	if total, estimate := tree.Cost([]*tree.Node{n}, m.usages); total > 0 {
		mark := ""
		if estimate {
			mark = "~"
		}
		parts = append(parts, mark+human.USD(total))
	}
	parts = append(parts, tokenParts(tokens, false)...)
	return append(parts, fmt.Sprintf("with the %d sessions under it", below))
}

func flow(label string, parts []string, w int) []string {
	if len(parts) == 0 {
		return nil
	}
	room := max(10, w-labelWidth)
	sep := sDim.Render(" · ")
	var lines []string
	line := ""
	for _, p := range parts {
		p = ansi.Truncate(p, room, "…")
		switch {
		case line == "":
			line = p
		case ansi.StringWidth(line)+3+ansi.StringWidth(p) <= room:
			line += sep + p
		default:
			lines = append(lines, line)
			line = p
		}
	}
	lines = append(lines, line)
	out := make([]string, len(lines))
	for i, l := range lines {
		indent := strings.Repeat(" ", labelWidth)
		if i == 0 {
			indent = sDim.Render(fmt.Sprintf("%-*s", labelWidth, label))
		}
		out[i] = indent + sText.Render(l)
	}
	return out
}

func modelTable(u store.Usage, w int) []string {
	if len(u.Models) < 2 {
		return nil
	}
	names := make([]string, 0, len(u.Models))
	for name := range u.Models {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return u.Models[names[i]].Output > u.Models[names[j]].Output })
	nameWidth := 0
	for _, name := range names {
		nameWidth = max(nameWidth, min(24, ansi.StringWidth(name)))
	}
	cell := func(n int64) string { return fmt.Sprintf("%8s", human.Tokens(n)) }
	indent := strings.Repeat(" ", labelWidth)
	columns := 4
	if labelWidth+nameWidth+8*4 > w {
		columns = 3
	}
	if labelWidth+nameWidth+8*3 > w {
		var out []string
		out = append(out, sDim.Render(fmt.Sprintf("%-*s", labelWidth, "models")))
		for _, name := range names {
			t := u.Models[name]
			out = append(out, indent+sText.Render(ansi.Truncate(name, w-labelWidth, "…")),
				indent+"  "+sText.Render(strings.Join(tokenParts(t, false), sDim.Render(" · "))))
		}
		return out
	}
	header := fmt.Sprintf("%-*s%8s%8s%8s", nameWidth, "", "in", "out", "cache")
	if columns == 4 {
		header = fmt.Sprintf("%-*s%8s%8s%8s%8s", nameWidth, "", "in", "out", "cache r", "cache w")
	}
	out := []string{sDim.Render(fmt.Sprintf("%-*s", labelWidth, "models")) + sDim.Render(header)}
	for _, name := range names {
		t := u.Models[name]
		line := fmt.Sprintf("%-*s", nameWidth, ansi.Truncate(name, nameWidth, "…")) + cell(t.Input) + cell(t.Output)
		if columns == 4 {
			line += cell(t.CacheRead) + cell(t.CacheWrite)
		} else {
			line += cell(t.CacheRead + t.CacheWrite)
		}
		out = append(out, indent+sText.Render(line))
	}
	return out
}
