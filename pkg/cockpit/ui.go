package cockpit

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/gitinfo"
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

const gitTTL = 5 * time.Second

var (
	styleNeeds   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true)
	styleWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("#00d787"))
	styleShell   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fafff"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("#b48cff")).Bold(true)
	styleAdd     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00d787"))
	styleDel     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f5f"))
	styleMod     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd75f"))
	styleCursor  = lipgloss.NewStyle().Reverse(true)
)

var glyphs = map[agents.State]string{
	agents.NeedsYou: styleNeeds.Render("⚠"),
	agents.Working:  styleWorking.Render("●"),
	agents.Shell:    styleShell.Render("$"),
	agents.Idle:     styleDim.Render("○"),
	agents.Unknown:  styleDim.Render("?"),
}

func plainGlyph(s agents.State) string { return [...]string{"⚠", "●", "$", "○", "?"}[s] }

type scanMsg struct {
	agents []agents.Agent
	panes  map[string]tmux.Pane
}

type gitMsg gitinfo.Info

type tickMsg struct{}

type model struct {
	mode    Mode
	session string
	all     bool // show every tmux session
	scanner *agents.Scanner
	t       *tmux.Client
	cp      *Cockpit

	agents []agents.Agent
	panes  map[string]tmux.Pane
	rows   []row
	cursor string // row key
	marked map[string]bool

	filter   string
	input    inputKind
	buf      string
	spawnFor string
	flash    string

	git     map[string]gitinfo.Info
	loading map[string]bool
	gitView int // 0 changes, 1 log, 2 worktrees

	width, height int
	lineRows      []int // row index per screen line, -1 when not a row
	offset        int
}

func Run(mode Mode, session string) error {
	t := tmux.New()
	if session == "" {
		var err error
		if session, err = CurrentSession(t); err != nil {
			return err
		}
	}
	m := &model{mode: mode, session: session, scanner: agents.NewScanner(), t: t, cp: New(t, session),
		marked: map[string]bool{}, git: map[string]gitinfo.Info{}, loading: map[string]bool{}}
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

func (m *model) scope() string {
	if m.all {
		return ""
	}
	return m.session
}

func (m *model) apply(s scanMsg) {
	m.agents, m.panes = s.agents, s.panes
	m.cp.load()
	m.rows = buildTree(treeInput{agents: m.agents, panes: m.panes, scope: m.scope(),
		borrowed: m.cp.State.Borrowed, slot: m.cp.State.Slot, filter: m.filter})
	for _, r := range m.rows {
		if r.key == m.cursor {
			return
		}
	}
	m.cursor = ""
	for _, r := range m.rows {
		if r.kind == agentRow {
			m.cursor = r.key
			return
		}
	}
	if len(m.rows) > 0 {
		m.cursor = m.rows[0].key
	}
}

func (m *model) current() (row, bool) {
	for _, r := range m.rows {
		if r.key == m.cursor {
			return r, true
		}
	}
	return row{}, false
}

// target is the pane a row stands for: the agent's, or the window's active one.
func (r row) target() (pane, name string) {
	switch r.kind {
	case agentRow:
		return r.agent.PaneID, r.agent.Name
	case windowRow:
		return r.window.ActivePane, r.window.Name
	}
	return "", ""
}

func (r row) dir() string {
	switch r.kind {
	case agentRow:
		return r.agent.Cwd
	case windowRow:
		return r.window.Path
	}
	return ""
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// loadGit fetches git state for the selected row in the background.
func (m *model) loadGit() tea.Cmd {
	r, ok := m.current()
	if m.mode != Sidebar || !ok {
		return nil
	}
	dir := r.dir()
	if dir == "" || m.loading[dir] || time.Since(m.git[dir].At) < gitTTL {
		return nil
	}
	m.loading[dir] = true
	return func() tea.Msg { return gitMsg(gitinfo.Load(dir)) }
}

func (m *model) Init() tea.Cmd { return tea.Batch(tick(), m.loadGit()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(func() tea.Msg { return m.scan() }, tick())
	case scanMsg:
		m.apply(msg)
		return m, m.loadGit()
	case gitMsg:
		m.git[msg.Dir] = gitinfo.Info(msg)
		delete(m.loading, msg.Dir)
	case tea.MouseMsg:
		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			return m.click(msg.Y)
		case msg.Button == tea.MouseButtonWheelUp:
			m.move(-1)
		case msg.Button == tea.MouseButtonWheelDown:
			m.move(1)
		}
		return m, m.loadGit()
	case tea.KeyMsg:
		if m.input != noInput {
			return m.typing(msg)
		}
		next, cmd := m.command(msg.String())
		return next, tea.Batch(cmd, m.loadGit())
	}
	return m, nil
}

func (m *model) move(d int) {
	for i, r := range m.rows {
		if r.key == m.cursor {
			if j := i + d; j >= 0 && j < len(m.rows) {
				m.cursor = m.rows[j].key
			}
			return
		}
	}
}

func (m *model) click(y int) (tea.Model, tea.Cmd) {
	if y < 0 || y >= len(m.lineRows) || m.lineRows[y] < 0 || m.lineRows[y] >= len(m.rows) {
		return m, nil
	}
	m.cursor = m.rows[m.lineRows[y]].key
	next, cmd := m.command("enter")
	return next, tea.Batch(cmd, m.loadGit())
}

func (m *model) command(k string) (tea.Model, tea.Cmd) {
	m.flash = ""
	r, ok := m.current()
	switch k {
	case "q", "ctrl+c", "esc":
		if k == "esc" && m.filter != "" {
			m.filter = ""
			m.apply(scanMsg{m.agents, m.panes})
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "!":
		m.nextNeedsYou()
	case "enter":
		pane, name := r.target()
		if !ok || pane == "" {
			if r.kind == agentRow {
				m.flash = "not running in tmux"
			}
			return m, nil
		}
		if m.mode == Popup {
			m.t.Jump(pane)
			return m, tea.Quit
		}
		if err := m.cp.Show(pane, name); err != nil {
			m.flash = err.Error()
		}
		m.apply(scanMsg{m.agents, m.panes})
	case "tab", "l", "right":
		if m.mode == Sidebar && m.cp.State.Borrowed != "" {
			m.t.SelectPane(m.cp.State.Borrowed)
		}
	case "g":
		if pane, _ := r.target(); ok && pane != "" {
			m.cp.Restore()
			m.t.Jump(pane)
			if m.mode == Popup {
				return m, tea.Quit
			}
		}
	case "a":
		m.all = !m.all
		m.apply(scanMsg{m.agents, m.panes})
	case "v":
		m.gitView = (m.gitView + 1) % 3
	case " ":
		if ok && r.kind == agentRow {
			m.marked[r.key] = !m.marked[r.key]
			m.move(1)
		}
	case "/":
		m.input, m.buf = filterInput, m.filter
	case "p":
		if len(m.targets()) > 0 {
			m.input, m.buf = promptInput, ""
		}
	case "n":
		m.input, m.buf = spawnNameInput, ""
	}
	return m, nil
}

func (m *model) nextNeedsYou() {
	start := 0
	for i, r := range m.rows {
		if r.key == m.cursor {
			start = i + 1
		}
	}
	for i := range m.rows {
		r := m.rows[(start+i)%len(m.rows)]
		if r.kind == agentRow && r.state == agents.NeedsYou {
			m.cursor = r.key
			return
		}
	}
	m.flash = "nobody needs you"
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
	}
	m.apply(scanMsg{m.agents, m.panes})
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
			if r, ok := m.current(); ok {
				m.buf = r.dir()
			}
		}
	case spawnDirInput:
		m.flash = m.spawn(m.spawnFor, buf)
	}
	return m, nil
}

// targets are the marked agents, or the agent under the cursor.
func (m *model) targets() []agents.Agent {
	var out []agents.Agent
	for _, a := range m.agents {
		if m.marked[key(a)] {
			out = append(out, a)
		}
	}
	if r, ok := m.current(); len(out) == 0 && ok && r.kind == agentRow {
		out = append(out, r.agent)
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
	// Keep the Claude profile of the agent the new one was spawned from.
	if r, ok := m.current(); ok && r.kind == agentRow && r.agent.ConfigDir != "" {
		cmd = "CLAUDE_CONFIG_DIR=" + shellQuote(r.agent.ConfigDir) + " " + cmd
	}
	pane, err := m.t.NewWindow(m.session+":", name, dir, cmd)
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

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if r := []rune(s); len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

// spread puts right at the far edge of a line of the given width.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(gap, 1)) + right
}

// ── View ────────────────────────────────────────────────

func (m *model) View() string {
	w := max(m.width, 20)
	if m.mode == Popup {
		w = min(w, 100) // keep ages next to names in a wide popup
	}

	title := "Claude · " + m.session
	if m.all {
		title = "Claude · all sessions"
	}
	c := map[agents.State]int{}
	for _, r := range m.rows {
		if r.kind == agentRow {
			c[r.state]++
		}
	}
	counts := fmt.Sprintf("⚠%d ●%d ○%d", c[agents.NeedsYou], c[agents.Working], c[agents.Idle])
	head := spread(styleHeader.Render(trunc(title, w-12)), styleDim.Render(counts), w)

	foot := m.footer(w)
	var gitLines []string
	if m.mode == Sidebar && m.height >= 24 {
		gitLines = m.gitPanel(w, m.height*2/5)
	}

	list, listRows := m.list(w)
	room := m.height - 1 - len(gitLines) - len(foot)
	if m.height == 0 {
		room = len(list)
	}
	list, listRows = m.scroll(list, listRows, max(room, 1))
	for len(list) < room { // keep the git panel anchored at the bottom
		list, listRows = append(list, ""), append(listRows, -1)
	}

	m.lineRows = append([]int{-1}, listRows...)
	for range len(gitLines) + len(foot) {
		m.lineRows = append(m.lineRows, -1)
	}
	out := append([]string{head}, list...)
	out = append(out, gitLines...)
	out = append(out, foot...)
	return strings.Join(out, "\n")
}

func (m *model) list(w int) (lines []string, rowIdx []int) {
	add := func(s string, i int) { lines, rowIdx = append(lines, s), append(rowIdx, i) }
	indent := ""
	if m.all {
		indent = " "
	}
	for i, r := range m.rows {
		sel := r.key == m.cursor
		var text string
		switch r.kind {
		case sessionRow:
			name := "session " + r.session
			if r.session == "" {
				name = "outside tmux"
			}
			add("", -1)
			text = styleHeader.Render("▣ " + name)
		case windowRow:
			label := trunc(r.window.Index+" "+r.window.Name, w-len(indent)-4)
			glyph := ""
			if r.agentsN > 0 {
				glyph = glyphs[r.state]
				if sel {
					glyph = plainGlyph(r.state)
				}
			} else if !sel {
				label = styleDim.Render(label)
			}
			text = spread(indent+label, glyph, w)
		case agentRow:
			mark := " "
			if m.marked[r.key] {
				mark = "✓"
			}
			if m.mode == Sidebar && r.agent.PaneID != "" && r.agent.PaneID == m.cp.State.Borrowed {
				mark = "▶"
			}
			glyph := glyphs[r.state]
			if sel {
				glyph = plainGlyph(r.state) // nested colors would cut the highlight short
			}
			a := age(r.agent.Since)
			name := trunc(r.agent.Name, w-len(indent)-8-len(a))
			text = spread(indent+"  "+mark+glyph+" "+name, a, w)
		}
		if sel {
			text = styleCursor.Render(text)
		}
		add(text, i)
		if r.kind == agentRow && r.state == agents.NeedsYou && r.agent.WaitingFor != "" {
			add(indent+"     "+styleNeeds.Render(trunc(r.agent.WaitingFor, w-len(indent)-5)), i)
		}
	}
	if len(m.rows) == 0 {
		add(styleDim.Render("no windows"), -1)
	}
	return lines, rowIdx
}

// scroll keeps the cursor's line visible within room lines.
func (m *model) scroll(lines []string, rowIdx []int, room int) ([]string, []int) {
	if len(lines) <= room {
		m.offset = 0
		return lines, rowIdx
	}
	cur := 0
	for i, idx := range rowIdx {
		if idx >= 0 && m.rows[idx].key == m.cursor {
			cur = i
			break
		}
	}
	if cur < m.offset {
		m.offset = cur
	} else if cur >= m.offset+room {
		m.offset = cur - room + 1
	}
	m.offset = min(max(m.offset, 0), len(lines)-room)
	return lines[m.offset : m.offset+room], rowIdx[m.offset : m.offset+room]
}

func (m *model) footer(w int) []string {
	switch m.input {
	case filterInput:
		return []string{"", "filter: " + m.buf + "█"}
	case promptInput:
		var names []string
		for _, a := range m.targets() {
			names = append(names, a.Name)
		}
		return []string{trunc("→ "+strings.Join(names, ", "), w), "prompt: " + m.buf + "█"}
	case spawnNameInput:
		return []string{"", "new agent name: " + m.buf + "█"}
	case spawnDirInput:
		return []string{"", "in: " + m.buf + "█"}
	}
	out := []string{styleDim.Render(trunc(m.flash, w))}
	if m.mode == Popup {
		return append(out, styleDim.Render(trunc("⏎ jump · p prompt · ␣ mark · n new · / filter · a all · ! next · esc", w)))
	}
	return append(out,
		styleDim.Render(trunc("⏎ show ⇥ focus g go p prompt n new", w)),
		styleDim.Render(trunc("/ filter a all v git ! next q close", w)))
}

func (m *model) gitPanel(w, h int) []string {
	tabs := []string{"Changes", "Log", "Worktrees"}
	for i, t := range tabs {
		if i == m.gitView {
			tabs[i] = styleHeader.Render(t)
		} else {
			tabs[i] = styleDim.Render(t)
		}
	}
	bar := "─ " + strings.Join(tabs, styleDim.Render(" │ ")) + " "
	lines := []string{bar + styleDim.Render(strings.Repeat("─", max(w-lipgloss.Width(bar), 0)))}

	r, ok := m.current()
	dir := r.dir()
	info, have := m.git[dir]
	switch {
	case !ok || dir == "":
		lines = append(lines, styleDim.Render("select a window or agent"))
	case !have:
		lines = append(lines, styleDim.Render("…"))
	case !info.Repo:
		lines = append(lines, styleDim.Render(trunc(filepath.Base(dir)+": not a git repo", w)))
	default:
		switch m.gitView {
		case 0:
			lines = append(lines, changes(info, w)...)
		case 1:
			for _, c := range info.Log {
				when := age(c.When)
				lines = append(lines, spread(styleMod.Render(c.Hash)+" "+trunc(c.Subject, w-len(c.Hash)-len(when)-2), styleDim.Render(when), w))
			}
		case 2:
			for _, wt := range info.Worktrees {
				mark := "  "
				if wt.Current {
					mark = styleWorking.Render("● ")
				}
				lines = append(lines, spread(mark+trunc(wt.Branch, w/2), styleDim.Render(trunc(filepath.Base(wt.Path), w/2-3)), w))
			}
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func changes(info gitinfo.Info, w int) []string {
	ab := ""
	if info.Ahead > 0 {
		ab += styleAdd.Render(fmt.Sprintf("↑%d", info.Ahead))
	}
	if info.Behind > 0 {
		ab += styleDel.Render(fmt.Sprintf("↓%d", info.Behind))
	}
	add, del, files := info.Totals()
	lines := []string{
		spread(trunc(info.Branch, w-8), ab, w),
		spread(styleAdd.Render(fmt.Sprintf("+%d", add))+"/"+styleDel.Render(fmt.Sprintf("-%d", del)), fmt.Sprintf("%d files", files), w),
	}
	if files == 0 {
		return append(lines, styleDim.Render("clean"))
	}
	section := func(title string, fs []gitinfo.File) {
		if len(fs) == 0 {
			return
		}
		lines = append(lines, styleShell.Render(fmt.Sprintf("%s (%d)", title, len(fs))))
		for i, f := range fs {
			if i == 5 && len(fs) > 6 {
				lines = append(lines, styleDim.Render(fmt.Sprintf("  +%d more", len(fs)-5)))
				break
			}
			st := string(f.Status)
			switch f.Status {
			case 'A':
				st = styleAdd.Render(st)
			case 'D':
				st = styleDel.Render(st)
			case 'M', 'R':
				st = styleMod.Render(st)
			}
			num := ""
			if f.Status != '?' {
				num = styleAdd.Render(fmt.Sprintf("+%d", f.Add)) + "/" + styleDel.Render(fmt.Sprintf("-%d", f.Del))
			}
			lines = append(lines, spread(st+" "+trunc(f.Path, w-lipgloss.Width(num)-4), num, w))
		}
	}
	section("Staged", info.Staged)
	section("Unstaged", info.Unstaged)
	section("Untracked", info.Untracked)
	return lines
}
