package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sadrishehu/hive/internal/store"
)

func color(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

var (
	cWorking   = color("#B7791F", "#E5C07B")
	cAttention = color("#C53030", "#E06C75")
	cIdle      = color("#2F855A", "#98C379")
	cRunning   = color("#2B6CB0", "#61AFEF")
	cDim       = color("#8A8F98", "#6B717D")
	cText      = color("#1F2328", "#D7DAE0")
	cSelect    = color("#DCE3EE", "#2C323C")
	cAccent    = color("#6B46C1", "#C678DD")
	cBorder    = color("#C8CCD2", "#3B4048")

	toolColors = map[string]lipgloss.AdaptiveColor{
		"claude":   color("#B8572F", "#D97757"),
		"opencode": color("#1E7E8A", "#56B6C2"),
		"copilot":  color("#3568D4", "#7AA2F7"),
		"codex":    color("#0F7A5C", "#10A37F"),
	}

	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sError  = lipgloss.NewStyle().Foreground(cAttention)
	sOK     = lipgloss.NewStyle().Foreground(cIdle)
	sBadge  = lipgloss.NewStyle().Foreground(color("#FFFFFF", "#1E2127")).Background(cAccent).Bold(true).Padding(0, 1)
	sKey    = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sRule   = lipgloss.NewStyle().Foreground(cBorder)
)

func toolStyle(tool string) lipgloss.Style {
	c, ok := toolColors[tool]
	if !ok {
		c = cAccent
	}
	return lipgloss.NewStyle().Foreground(c)
}

// glyph is a session's status mark and its color.
func glyph(s store.Session) (string, lipgloss.Style) {
	if !s.Live() {
		return "○", sDim
	}
	switch s.Status {
	case store.StatusWorking:
		return "●", lipgloss.NewStyle().Foreground(cWorking)
	case store.StatusAttention:
		return "◆", lipgloss.NewStyle().Foreground(cAttention).Bold(true)
	case store.StatusIdle:
		return "◉", lipgloss.NewStyle().Foreground(cIdle)
	}
	return "◌", lipgloss.NewStyle().Foreground(cRunning)
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

func contextStyle(pct int) lipgloss.Style {
	switch {
	case pct < 50:
		return lipgloss.NewStyle().Foreground(cIdle)
	case pct <= 75:
		return lipgloss.NewStyle().Foreground(cWorking)
	}
	return lipgloss.NewStyle().Foreground(cAttention)
}

func bar(pct, width int) string {
	filled := min(width, max(0, int(math.Round(float64(pct)*float64(width)/100))))
	return contextStyle(pct).Render(strings.Repeat("▓", filled)) + sRule.Render(strings.Repeat("░", width-filled))
}
