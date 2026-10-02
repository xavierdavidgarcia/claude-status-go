package cockpit

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/xgarcia/claude-status-go/pkg/agents"
)

// Catppuccin Mocha, the palette herdr ships by default.
var (
	cSurface0 = lipgloss.Color("#313244")
	cSurface1 = lipgloss.Color("#45475a")
	cOverlay0 = lipgloss.Color("#6c7086")
	cSubtext0 = lipgloss.Color("#a6adc8")
	cText     = lipgloss.Color("#cdd6f4")
	cMauve    = lipgloss.Color("#cba6f7")
	cGreen    = lipgloss.Color("#a6e3a1")
	cYellow   = lipgloss.Color("#f9e2af")
	cPeach    = lipgloss.Color("#fab387")
	cRed      = lipgloss.Color("#f38ba8")
	cBlue     = lipgloss.Color("#89b4fa")
)

var (
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sDim    = lipgloss.NewStyle().Foreground(cOverlay0)
	sSub    = lipgloss.NewStyle().Foreground(cSubtext0)
	sAccent = lipgloss.NewStyle().Foreground(cMauve)
	sBranch = lipgloss.NewStyle().Foreground(cMauve).Faint(true)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sPeach  = lipgloss.NewStyle().Foreground(cPeach)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sRedB   = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	sBlue   = lipgloss.NewStyle().Foreground(cBlue)
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// stateGlyph is the agent icon; working agents animate.
func stateGlyph(s agents.State, frame int) seg {
	switch s {
	case agents.NeedsYou:
		return seg{sRedB, "!"}
	case agents.Working:
		return seg{sYellow, spinner[frame%len(spinner)]}
	case agents.Shell:
		return seg{sBlue, "$"}
	case agents.Idle:
		return seg{sGreen, "✓"}
	}
	return seg{sDim, "·"}
}

func stateWord(s agents.State) seg {
	switch s {
	case agents.NeedsYou:
		return seg{sRed, "needs you"}
	case agents.Working:
		return seg{sDim, "working"}
	case agents.Shell:
		return seg{sDim, "shell"}
	case agents.Idle:
		return seg{sDim, "idle"}
	}
	return seg{sDim, ""}
}

// dot marks a space with its most urgent agent's state.
func dot(s agents.State) seg {
	switch s {
	case agents.NeedsYou:
		return seg{sRed, "●"}
	case agents.Working:
		return seg{sYellow, "●"}
	case agents.Shell:
		return seg{sBlue, "●"}
	case agents.Idle:
		return seg{sGreen, "●"}
	}
	return seg{sDim, ""}
}

// seg is a styled piece of a line; rows render every piece on the same
// background so a selected row's highlight isn't cut by inner color resets.
type seg struct {
	style lipgloss.Style
	text  string
}

// line renders left and right segments across width, highlighted when sel.
func line(width int, sel bool, left, right []seg) string {
	render := func(s seg) string {
		if sel {
			s.style = s.style.Background(cSurface0)
		}
		return s.style.Render(s.text)
	}
	var l, r string
	lw, rw := 0, 0
	for _, s := range left {
		l += render(s)
		lw += lipgloss.Width(s.text)
	}
	for _, s := range right {
		r += render(s)
		rw += lipgloss.Width(s.text)
	}
	gap := width - lw - rw
	if gap < 1 {
		gap = 1
	}
	pad := lipgloss.NewStyle()
	if sel {
		pad = pad.Background(cSurface0)
	}
	return l + pad.Render(blanks(gap)) + r
}

func blanks(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
