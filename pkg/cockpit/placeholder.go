package cockpit

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

// Placeholder runs in the slot pane. While an agent is borrowed, the slot sits
// in that agent's home window; Enter there brings the agent back.
func Placeholder(session string) error {
	t := tmux.New()
	_, err := tea.NewProgram(&placeholder{cp: New(t, session), t: t, self: os.Getenv("TMUX_PANE")}, tea.WithAltScreen()).Run()
	return err
}

type placeholder struct {
	cp            *Cockpit
	t             *tmux.Client
	self          string
	width, height int
}

func (p *placeholder) Init() tea.Cmd { return tick() }

func (p *placeholder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case tickMsg:
		p.cp.load()
		return p, tick()
	case tea.KeyMsg:
		if msg.Type == tea.KeyEnter && p.away() {
			agent := p.cp.State.Borrowed
			p.cp.Restore()
			p.t.SelectPane(agent)
		}
	}
	return p, nil
}

// away reports whether this slot is parked in a borrowed agent's window.
func (p *placeholder) away() bool {
	return p.cp.State.Borrowed != "" && p.cp.State.Slot == p.self
}

func (p *placeholder) View() string {
	msg := styleDim.Render("Select an agent on the left.")
	if p.away() {
		msg = styleHeader.Render(p.cp.State.BorrowedName) + " is in the cockpit.\n\n" +
			styleDim.Render("Enter: bring it back here")
	}
	return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, msg)
}

// Count prints a tmux status segment, e.g. "⚠ 2 ● 5 ○ 10".
func Count() {
	c := agents.Counts(agents.NewScanner().Scan())
	var parts []string
	if n := c[agents.NeedsYou]; n > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=red,bold]⚠ %d#[default]", n))
	}
	if n := c[agents.Working]; n > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=green]● %d#[default]", n))
	}
	if n := c[agents.Idle]; n > 0 {
		parts = append(parts, fmt.Sprintf("○ %d", n))
	}
	fmt.Print(strings.Join(parts, " "))
}
