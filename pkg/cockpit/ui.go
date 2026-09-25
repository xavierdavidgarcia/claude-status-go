package cockpit

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

type Mode int

const (
	Popup Mode = iota
	Sidebar
)

type inputKind int

const (
	noInput inputKind = iota
	filterInput
	promptInput
	spawnNameInput
	spawnDirInput
)

var (
	styleNeeds   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true)
	styleWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("#00d787"))
	styleShell   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fafff"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("#b48cff")).Bold(true)
	styleCursor  = lipgloss.NewStyle().Reverse(true)
)

var glyphs = map[agents.State]string{
	agents.NeedsYou: styleNeeds.Render("⚠"),
	agents.Working:  styleWorking.Render("●"),
	agents.Shell:    styleShell.Render("$"),
	agents.Idle:     styleDim.Render("○"),
	agents.Unknown:  styleDim.Render("?"),
}

type scanMsg struct {
	agents []agents.Agent
	panes  map[string]tmux.Pane
}

type tickMsg struct{}

type model struct {
	mode     Mode
	scanner  *agents.Scanner
	t        *tmux.Client
	cp       *Cockpit
	all      []agents.Agent
	panes    map[string]tmux.Pane
	cursor   string // pane id, or "pid:<n>" for agents outside tmux
	marked   map[string]bool
	filter   string
	input    inputKind
	buf      string
	spawnFor string // name entered before the directory prompt
	flash    string
	width    int
	height   int
	rows     []int // agent index per rendered line, -1 for headers
	offset   int   // first list line shown
}

func Run(mode Mode) error {
	t := tmux.New()
	m := &model{mode: mode, scanner: agents.NewScanner(), t: t, cp: New(t), marked: map[string]bool{}}
	m.apply(m.scan())
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	// kill-pane sends SIGHUP; quit cleanly so the borrowed agent goes home.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() { <-hup; p.Quit() }()
	_, err := p.Run()
	if mode == Sidebar {
		m.cp.Close()
	}
	return err
}

func (m *model) scan() scanMsg {
	return scanMsg{agents: m.scanner.Scan(), panes: m.t.Panes()}
}

func key(a agents.Agent) string {
	if a.PaneID != "" {
		return a.PaneID
	}
	return fmt.Sprintf("pid:%d", a.PID)
}

func (m *model) apply(s scanMsg) {
	m.all, m.panes = s.agents, s.panes
	vis := m.visible()
	for _, a := range vis {
		if key(a) == m.cursor {
			return
		}
	}
	if len(vis) > 0 {
		m.cursor = key(vis[0])
	}
}

func (m *model) visible() []agents.Agent {
	if m.filter == "" {
		return m.all
	}
	var out []agents.Agent
	f := strings.ToLower(m.filter)
	for _, a := range m.all {
		if strings.Contains(strings.ToLower(a.Name+" "+a.Cwd), f) {
			out = append(out, a)
		}
	}
	return out
}

func (m *model) current() (agents.Agent, bool) {
	for _, a := range m.visible() {
		if key(a) == m.cursor {
			return a, true
		}
	}
	return agents.Agent{}, false
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd { return tick() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(func() tea.Msg { return m.scan() }, tick())
	case scanMsg:
		m.apply(msg)
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			return m.click(msg.Y)
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.move(-1)
		} else if msg.Button == tea.MouseButtonWheelDown {
			m.move(1)
		}
	case tea.KeyMsg:
		if m.input != noInput {
			return m.typing(msg)
		}
		return m.command(msg.String())
	}
	return m, nil
}

func (m *model) move(d int) {
	vis := m.visible()
	for i, a := range vis {
		if key(a) == m.cursor {
			if j := i + d; j >= 0 && j < len(vis) {
				m.cursor = key(vis[j])
			}
			return
		}
	}
}

func (m *model) click(y int) (tea.Model, tea.Cmd) {
	if y < 0 || y >= len(m.rows) || m.rows[y] < 0 {
		return m, nil
	}
	vis := m.visible()
	if m.rows[y] >= len(vis) {
		return m, nil
	}
	m.cursor = key(vis[m.rows[y]])
	return m.command("enter")
}

func (m *model) command(k string) (tea.Model, tea.Cmd) {
	m.flash = ""
	a, ok := m.current()
	switch k {
	case "q", "ctrl+c", "esc":
		if k == "esc" && m.filter != "" {
			m.filter = ""
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "enter":
		if !ok {
			return m, nil
		}
		if a.PaneID == "" {
			m.flash = "not running in tmux"
			return m, nil
		}
		if m.mode == Popup {
			m.t.Jump(a.PaneID)
			return m, tea.Quit
		}
		if err := m.cp.Show(a.PaneID, a.Name); err != nil {
			m.flash = err.Error()
		}
	case "tab", "l", "right":
		if m.mode == Sidebar && m.cp.State.Borrowed != "" {
			m.t.SelectPane(m.cp.State.Borrowed)
		}
	case "g":
		if ok && a.PaneID != "" {
			m.cp.Restore()
			m.t.Jump(a.PaneID)
			if m.mode == Popup {
				return m, tea.Quit
			}
		}
	case " ":
		if ok {
			m.marked[key(a)] = !m.marked[key(a)]
			m.move(1)
		}
	case "/":
		m.input, m.buf = filterInput, m.filter
	case "p":
		if ok {
			m.input, m.buf = promptInput, ""
		}
	case "n":
		m.input, m.buf = spawnNameInput, ""
	}
	return m, nil
}

func (m *model) typing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.input == filterInput {
			m.filter = ""
		}
		m.input, m.buf = noInput, ""
	case tea.KeyBackspace:
		if r := []rune(m.buf); len(r) > 0 {
			m.buf = string(r[:len(r)-1])
		}
	case tea.KeyEnter:
		return m.submit()
	case tea.KeySpace:
		m.buf += " "
	case tea.KeyRunes:
		m.buf += string(msg.Runes)
	}
	if m.input == filterInput {
		m.filter = m.buf
		m.apply(scanMsg{m.all, m.panes})
	}
	return m, nil
}

func (m *model) submit() (tea.Model, tea.Cmd) {
	in, buf := m.input, strings.TrimSpace(m.buf)
	m.input, m.buf = noInput, ""
	switch in {
	case promptInput:
		if buf != "" {
			m.flash = m.sendPrompt(buf)
		}
	case spawnNameInput:
		if buf != "" {
			m.spawnFor = buf
			m.input = spawnDirInput
			if a, ok := m.current(); ok {
				m.buf = a.Cwd
			}
		}
	case spawnDirInput:
		m.flash = m.spawn(m.spawnFor, buf)
	}
	return m, nil
}

// targets are the marked agents, or the one under the cursor.
func (m *model) targets() []agents.Agent {
	var out []agents.Agent
	for _, a := range m.all {
		if m.marked[key(a)] {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		if a, ok := m.current(); ok {
			out = append(out, a)
		}
	}
	return out
}

// sendPrompt re-reads state right before typing: text sent into a permission
// dialog could select an option.
func (m *model) sendPrompt(text string) string {
	fresh := map[int]agents.Agent{}
	for _, a := range m.scanner.Scan() {
		fresh[a.PID] = a
	}
	var sent, skipped []string
	for _, t := range m.targets() {
		a, ok := fresh[t.PID]
		switch {
		case !ok || a.PaneID == "":
			skipped = append(skipped, t.Name+" (gone)")
		case a.State != agents.Idle && a.State != agents.Working:
			skipped = append(skipped, a.Name+" ("+a.State.String()+")")
		case m.t.Send(a.PaneID, text) != nil:
			skipped = append(skipped, a.Name+" (send failed)")
		default:
			sent = append(sent, a.Name)
		}
	}
	m.marked = map[string]bool{}
	msg := "sent: " + strings.Join(sent, ", ")
	if len(skipped) > 0 {
		msg += "  skipped: " + strings.Join(skipped, ", ")
	}
	return msg
}

func (m *model) spawn(name, dir string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := fmt.Sprintf("claude -w %s -n %s", shellQuote(name), shellQuote(name))
	// Keep the same Claude profile as the agent the new one was spawned from.
	if a, ok := m.current(); ok && a.ConfigDir != "" {
		cmd = "CLAUDE_CONFIG_DIR=" + shellQuote(a.ConfigDir) + " " + cmd
	}
	pane, err := m.t.NewWindow("", name, dir, cmd)
	if err != nil {
		return "spawn failed: " + err.Error()
	}
	if m.mode == Sidebar {
		m.cp.Show(pane, name)
	}
	return "started " + name
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (m *model) View() string {
	var lines []string
	var idxs []int
	cursorLine := 0
	line := func(s string, idx int) {
		lines = append(lines, s)
		idxs = append(idxs, idx)
	}

	c := agents.Counts(m.all)
	line(styleHeader.Render("Claude agents")+styleDim.Render(fmt.Sprintf("  ⚠%d ●%d ○%d", c[agents.NeedsYou], c[agents.Working], c[agents.Idle])), -1)

	vis := m.visible()
	last := agents.State(-1)
	for i, a := range vis {
		if a.State != last {
			last = a.State
			line("", -1)
			line(styleHeader.Render(strings.ToUpper(a.State.String())), -1)
		}
		if key(a) == m.cursor {
			cursorLine = len(lines)
		}
		line(m.row(a), i)
		if a.State == agents.NeedsYou && a.WaitingFor != "" {
			line("    "+styleDim.Render(a.WaitingFor), i)
		}
	}
	if len(vis) == 0 {
		line(styleDim.Render("no agents"), -1)
	}

	footer := len(lines)
	line("", -1)
	switch m.input {
	case filterInput:
		line("filter: "+m.buf+"█", -1)
	case promptInput:
		line(fmt.Sprintf("prompt → %d: %s█", len(m.targets()), m.buf), -1)
	case spawnNameInput:
		line("new agent name: "+m.buf+"█", -1)
	case spawnDirInput:
		line("in directory: "+m.buf+"█", -1)
	default:
		if m.flash != "" {
			line(styleDim.Render(m.flash), -1)
		}
		help := "enter show · tab focus · g go · p prompt · space mark · n new · / filter · q quit"
		if m.mode == Popup {
			help = "enter jump · p prompt · space mark · n new · / filter · esc close"
		}
		line(styleDim.Render(help), -1)
	}
	return m.window(lines, idxs, footer, cursorLine)
}

// window keeps the title and footer fixed and scrolls the list so the cursor
// stays visible. It also records which agent each screen line shows, for clicks.
func (m *model) window(lines []string, idxs []int, footer, cursor int) string {
	head, body := lines[:1], lines[1:footer]
	foot := lines[footer:]
	room := m.height - len(head) - len(foot)
	if m.height == 0 || room >= len(body) {
		m.rows = idxs
		return strings.Join(lines, "\n")
	}
	room = max(room, 1)
	cursor-- // index within body
	if cursor < m.offset {
		m.offset = cursor
	} else if cursor >= m.offset+room {
		m.offset = cursor - room + 1
	}
	m.offset = min(max(m.offset, 0), len(body)-room)
	shown := body[m.offset : m.offset+room]
	m.rows = append(append(append([]int{}, idxs[:1]...), idxs[1+m.offset:1+m.offset+room]...), idxs[footer:]...)
	return strings.Join(append(append(append([]string{}, head...), shown...), foot...), "\n")
}

func trunc(s string, w int) string {
	if r := []rune(s); len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

func stripStyle(s agents.State) string {
	return [...]string{"⚠", "●", "$", "○", "?"}[s]
}

func (m *model) row(a agents.Agent) string {
	mark := " "
	if m.marked[key(a)] {
		mark = "✓"
	}
	if m.mode == Sidebar && a.PaneID != "" && a.PaneID == m.cp.State.Borrowed {
		mark = "▶"
	}
	where := ""
	if p, ok := m.panes[a.PaneID]; ok {
		where = p.Location()
	} else if a.PaneID == "" {
		where = "no tmux"
	}
	name := a.Name
	glyph := glyphs[a.State]
	if key(a) == m.cursor {
		glyph = stripStyle(a.State) // nested colors would cut the highlight short
	}
	var text string
	if m.mode == Sidebar {
		w := max(m.width-12, 8)
		text = fmt.Sprintf("%s %s %-*s %4s", mark, glyph, w, trunc(name, w), age(a.Since))
	} else {
		text = fmt.Sprintf("%s %s %-28s %-16s %-10s %4s", mark, glyph, trunc(name, 28), trunc(a.Project(), 16), where, age(a.Since))
	}
	if key(a) == m.cursor {
		return styleCursor.Render(text)
	}
	return text
}
