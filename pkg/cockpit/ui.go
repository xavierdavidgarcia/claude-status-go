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

type focus int

const (
	fSpaces focus = iota
	fAgents
	fGit
)

type inputKind int

const (
	noInput inputKind = iota
	filterInput
	promptInput
	spawnNameInput
	spawnDirInput
)

const (
	gitTTL    = 5 * time.Second
	branchTTL = 15 * time.Second
	showDelay = 120 * time.Millisecond // let the cursor settle before swapping panes
)

var gitTabs = []string{"changes", "log", "worktrees"}

type (
	scanMsg struct {
		agents []agents.Agent
		panes  map[string]tmux.Pane
	}
	gitMsg    gitinfo.Info
	branchMsg struct{ dir, branch string }
	showMsg   struct{ pane string }
	tickMsg   struct{}
	spinMsg   struct{}
)

type branch struct {
	name string
	at   time.Time
}

type model struct {
	session string // tmux session the cockpit lives in
	scanner *agents.Scanner
	t       *tmux.Client
	cp      *Cockpit

	agents []agents.Agent
	panes  map[string]tmux.Pane
	spaces []space

	focus    focus
	spaceSel string // space name
	agentSel string // agent key
	gitTab   int
	gitSel   [3]int
	marked   map[string]bool

	filter   string
	input    inputKind
	buf      string
	spawnFor string
	flash    string

	git      map[string]gitinfo.Info
	loading  map[string]bool
	branches map[string]branch

	width, height int
	frame         int
	clicks        []click // what each screen line selects
}

type click struct {
	focus focus
	index int // -1: nothing
}

func Run(session string) error {
	t := tmux.New()
	if session == "" {
		var err error
		if session, err = CurrentSession(t); err != nil {
			return err
		}
	}
	m := &model{session: session, scanner: agents.NewScanner(), t: t, cp: New(t, session),
		spaceSel: session, focus: fAgents, marked: map[string]bool{},
		git: map[string]gitinfo.Info{}, loading: map[string]bool{}, branches: map[string]branch{}}
	m.apply(m.scan())
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	// kill-pane sends SIGHUP; quit cleanly so the borrowed agent goes home.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() { <-hup; p.Quit() }()
	_, err := p.Run()
	m.cp.Close()
	return err
}

func (m *model) scan() scanMsg { return scanMsg{agents: m.scanner.Scan(), panes: m.t.Panes()} }

func key(a agents.Agent) string {
	if a.PaneID != "" {
		return a.PaneID
	}
	return fmt.Sprintf("pid:%d", a.PID)
}

func (m *model) apply(s scanMsg) {
	m.agents, m.panes = s.agents, s.panes
	m.cp.load()
	m.spaces = buildSpaces(spaceInput{agents: m.agents, panes: m.panes,
		borrowed: m.cp.State.Borrowed, slot: m.cp.State.Slot, filter: m.filter})
	if _, ok := m.space(); !ok && len(m.spaces) > 0 {
		m.spaceSel = m.spaces[0].Name
	}
	sp, _ := m.space()
	for _, it := range sp.Agents {
		if key(it.Agent) == m.agentSel {
			return
		}
	}
	m.agentSel = ""
	if len(sp.Agents) > 0 {
		m.agentSel = key(sp.Agents[0].Agent)
	}
}

func (m *model) space() (space, bool) {
	for _, sp := range m.spaces {
		if sp.Name == m.spaceSel {
			return sp, true
		}
	}
	return space{}, false
}

func (m *model) agent() (item, bool) {
	sp, _ := m.space()
	for _, it := range sp.Agents {
		if key(it.Agent) == m.agentSel {
			return it, true
		}
	}
	return item{}, false
}

// dir is what the git panel describes: the agent's repo, else the space's.
func (m *model) dir() string {
	if it, ok := m.agent(); ok {
		return it.Cwd
	}
	sp, _ := m.space()
	return sp.Path
}

// ── commands (background work) ──────────────────────────

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func spin() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m *model) loadGit() tea.Cmd {
	dir := m.dir()
	if dir == "" || m.loading[dir] || time.Since(m.git[dir].At) < gitTTL {
		return nil
	}
	m.loading[dir] = true
	return func() tea.Msg { return gitMsg(gitinfo.Load(dir)) }
}

func (m *model) loadBranches() tea.Cmd {
	var cmds []tea.Cmd
	for _, sp := range m.spaces {
		dir := sp.Path
		if dir == "" || time.Since(m.branches[dir].at) < branchTTL {
			continue
		}
		m.branches[dir] = branch{name: m.branches[dir].name, at: time.Now()}
		cmds = append(cmds, func() tea.Msg { return branchMsg{dir, gitinfo.Branch(dir)} })
	}
	return tea.Batch(cmds...)
}

// follow schedules showing the selection once the cursor has settled.
func (m *model) follow() tea.Cmd {
	pane := m.previewPane()
	if pane == "" || pane == m.cp.State.Borrowed {
		return nil
	}
	return tea.Tick(showDelay, func(time.Time) tea.Msg { return showMsg{pane} })
}

func (m *model) previewPane() string {
	if m.focus == fSpaces {
		sp, _ := m.space()
		p, _ := sp.preview()
		return p
	}
	if it, ok := m.agent(); ok {
		return it.PaneID
	}
	return ""
}

func (m *model) nameOf(pane string) string {
	for _, a := range m.agents {
		if a.PaneID == pane {
			return a.Name
		}
	}
	if p, ok := m.panes[pane]; ok {
		return p.WindowName
	}
	return pane
}

// ── update ──────────────────────────────────────────────

func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), spin(), m.loadGit(), m.loadBranches(), m.follow())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinMsg:
		m.frame++
		return m, spin()
	case tickMsg:
		return m, tea.Batch(func() tea.Msg { return m.scan() }, tick())
	case scanMsg:
		m.apply(msg)
		return m, tea.Batch(m.loadGit(), m.loadBranches())
	case gitMsg:
		m.git[msg.Dir] = gitinfo.Info(msg)
		delete(m.loading, msg.Dir)
	case branchMsg:
		m.branches[msg.dir] = branch{name: msg.branch, at: time.Now()}
	case showMsg:
		if msg.pane == m.previewPane() { // still selected
			if err := m.cp.Show(msg.pane, m.nameOf(msg.pane)); err != nil {
				m.flash = err.Error()
			}
		}
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case tea.KeyMsg:
		if m.input != noInput {
			return m.typing(msg)
		}
		return m.key(msg.String())
	}
	return m, nil
}

func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		return m.move(-1)
	case msg.Button == tea.MouseButtonWheelDown:
		return m.move(1)
	case msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft:
		return nil
	}
	if msg.Y < 0 || msg.Y >= len(m.clicks) || m.clicks[msg.Y].index < 0 {
		return nil
	}
	c := m.clicks[msg.Y]
	m.focus = c.focus
	switch c.focus {
	case fSpaces:
		m.spaceSel = m.spaces[c.index].Name
		m.apply(scanMsg{m.agents, m.panes})
	case fAgents:
		sp, _ := m.space()
		m.agentSel = key(sp.Agents[c.index].Agent)
	case fGit:
		m.gitSel[m.gitTab] = c.index
	}
	return tea.Batch(m.follow(), m.loadGit())
}

// move steps the cursor of the focused section.
func (m *model) move(d int) tea.Cmd {
	switch m.focus {
	case fSpaces:
		for i, sp := range m.spaces {
			if sp.Name == m.spaceSel {
				if j := i + d; j >= 0 && j < len(m.spaces) {
					m.spaceSel = m.spaces[j].Name
					m.agentSel = ""
					m.apply(scanMsg{m.agents, m.panes})
				}
				break
			}
		}
	case fAgents:
		sp, _ := m.space()
		for i, it := range sp.Agents {
			if key(it.Agent) == m.agentSel {
				if j := i + d; j >= 0 && j < len(sp.Agents) {
					m.agentSel = key(sp.Agents[j].Agent)
				}
				break
			}
		}
	case fGit:
		n := len(m.gitItems())
		m.gitSel[m.gitTab] = min(max(m.gitSel[m.gitTab]+d, 0), max(n-1, 0))
		return nil
	}
	return tea.Batch(m.follow(), m.loadGit())
}

func (m *model) key(k string) (tea.Model, tea.Cmd) {
	m.flash = ""
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.apply(scanMsg{m.agents, m.panes})
			return m, nil
		}
		return m, tea.Quit
	case "j", "down":
		return m, m.move(1)
	case "k", "up":
		return m, m.move(-1)
	case "tab":
		m.focus = (m.focus + 1) % 3
		return m, m.follow()
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		return m, m.follow()
	case "left", "h":
		if m.focus == fGit {
			m.gitTab = (m.gitTab + 2) % 3
		} else if m.focus == fAgents {
			m.focus = fSpaces
		}
	case "right", "l":
		switch m.focus {
		case fGit:
			m.gitTab = (m.gitTab + 1) % 3
		case fSpaces:
			m.focus = fAgents
			return m, m.follow()
		case fAgents:
			m.typeInto()
		}
	case "enter":
		switch m.focus {
		case fSpaces:
			m.focus = fAgents
			return m, m.follow()
		case fAgents:
			m.typeInto()
		case fGit:
			return m, m.openGitItem()
		}
	case "g":
		if pane := m.previewPane(); pane != "" {
			m.cp.Restore()
			m.t.Jump(pane)
		}
	case "!":
		return m, m.nextNeedsYou()
	case " ":
		if it, ok := m.agent(); ok {
			m.marked[key(it.Agent)] = !m.marked[key(it.Agent)]
			return m, m.move(1)
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

// typeInto puts the keyboard in the shown agent's pane.
func (m *model) typeInto() {
	if it, ok := m.agent(); ok && it.PaneID != "" {
		m.cp.Show(it.PaneID, it.Name)
		m.t.SelectPane(it.PaneID)
	}
}

func (m *model) nextNeedsYou() tea.Cmd {
	type pos struct{ sp, ag string }
	var all []pos
	cur := -1
	for _, sp := range m.spaces {
		for _, it := range sp.Agents {
			if sp.Name == m.spaceSel && key(it.Agent) == m.agentSel {
				cur = len(all)
			}
			if it.State == agents.NeedsYou || (sp.Name == m.spaceSel && key(it.Agent) == m.agentSel) {
				all = append(all, pos{sp.Name, key(it.Agent)})
			}
		}
	}
	for i := 1; i <= len(all); i++ {
		p := all[(max(cur, 0)+i)%len(all)]
		if p.sp == m.spaceSel && p.ag == m.agentSel {
			continue
		}
		m.spaceSel, m.agentSel, m.focus = p.sp, p.ag, fAgents
		return tea.Batch(m.follow(), m.loadGit())
	}
	m.flash = "nobody needs you"
	return nil
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
			m.input, m.buf = spawnDirInput, m.dir()
		}
	case spawnDirInput:
		m.flash = m.spawn(m.spawnFor, buf)
	}
	return m, nil
}

// targets are the marked agents, or the selected one.
func (m *model) targets() []agents.Agent {
	var out []agents.Agent
	for _, a := range m.agents {
		if m.marked[key(a)] {
			out = append(out, a)
		}
	}
	if it, ok := m.agent(); len(out) == 0 && ok {
		out = append(out, it.Agent)
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
	if it, ok := m.agent(); ok && it.ConfigDir != "" {
		cmd = "CLAUDE_CONFIG_DIR=" + shellQuote(it.ConfigDir) + " " + cmd
	}
	target := m.spaceSel
	if target == "" {
		target = m.session
	}
	pane, err := m.t.NewWindow(target+":", name, dir, cmd)
	if err != nil {
		return "spawn failed: " + err.Error()
	}
	m.cp.Show(pane, name)
	return "started " + name
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── git panel items ─────────────────────────────────────

type gitItem struct {
	file   *gitinfo.File
	staged bool
	commit *gitinfo.Commit
	wt     *gitinfo.Worktree
}

func (m *model) gitItems() []gitItem {
	info := m.git[m.dir()]
	var out []gitItem
	switch m.gitTab {
	case 0:
		for i := range info.Staged {
			out = append(out, gitItem{file: &info.Staged[i], staged: true})
		}
		for i := range info.Unstaged {
			out = append(out, gitItem{file: &info.Unstaged[i]})
		}
		for i := range info.Untracked {
			out = append(out, gitItem{file: &info.Untracked[i]})
		}
	case 1:
		for i := range info.Log {
			out = append(out, gitItem{commit: &info.Log[i]})
		}
	case 2:
		for i := range info.Worktrees {
			out = append(out, gitItem{wt: &info.Worktrees[i]})
		}
	}
	return out
}

// openGitItem shows a diff or commit in a popup, or goes to a worktree's agent.
func (m *model) openGitItem() tea.Cmd {
	items := m.gitItems()
	i := m.gitSel[m.gitTab]
	if i >= len(items) {
		return nil
	}
	it, dir := items[i], m.dir()
	pager := " | less -R"
	switch {
	case it.file != nil && it.file.Status == '?':
		m.popup(dir, "less "+shellQuote(it.file.Path))
	case it.file != nil:
		cached := ""
		if it.staged {
			cached = " --cached"
		}
		m.popup(dir, "git diff --color=always"+cached+" -- "+shellQuote(it.file.Path)+pager)
	case it.commit != nil:
		m.popup(dir, "git show --color=always "+it.commit.Hash+pager)
	case it.wt != nil:
		for _, sp := range m.spaces {
			for _, a := range sp.Agents {
				if a.Cwd == it.wt.Path || strings.HasPrefix(a.Cwd, it.wt.Path+"/") {
					m.spaceSel, m.agentSel, m.focus = sp.Name, key(a.Agent), fAgents
					return tea.Batch(m.follow(), m.loadGit())
				}
			}
		}
		pane, err := m.t.NewWindow(m.session+":", filepath.Base(it.wt.Path), it.wt.Path, "")
		if err != nil {
			m.flash = err.Error()
			return nil
		}
		m.cp.Show(pane, filepath.Base(it.wt.Path))
	}
	return nil
}

func (m *model) popup(dir, cmd string) {
	if err := m.t.Popup(dir, cmd); err != nil {
		m.flash = "popup: " + err.Error()
	}
}

// ── view ────────────────────────────────────────────────

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

// block is a section's rendered lines with what each line selects.
type block struct {
	lines []string
	idx   []int
}

func (b *block) add(s string, i int) { b.lines, b.idx = append(b.lines, s), append(b.idx, i) }

// fit scrolls b to room lines keeping the lines of item sel visible.
func (b block) fit(room, sel int) block {
	if len(b.lines) <= room {
		return b
	}
	first, last := -1, -1
	for i, x := range b.idx {
		if x == sel {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	off := 0
	if last >= room {
		off = last - room + 1
	}
	if first >= 0 && first < off {
		off = first
	}
	off = min(off, len(b.lines)-room)
	return block{b.lines[off : off+room], b.idx[off : off+room]}
}

func (m *model) header(title string, f focus, w int, right []seg) string {
	st := sDim
	if m.focus == f {
		st = sAccent.Bold(true)
	}
	return line(w, false, []seg{{st, " " + title}}, right)
}

func (m *model) View() string {
	w, h := max(m.width, 24), max(m.height, 12)

	// Spaces: two lines each, at most a third of the screen.
	var sb block
	selSpace := 0
	for i, sp := range m.spaces {
		sel := sp.Name == m.spaceSel
		if sel {
			selSpace = i
		}
		hl := sel && m.focus == fSpaces
		bar := seg{sDim, " "}
		if sel {
			bar = seg{sAccent, "▎"}
		}
		name := sp.Name
		if name == "" {
			name = "outside tmux"
		}
		right := []seg{}
		if n := len(sp.Agents); n > 0 {
			right = []seg{{sDim, fmt.Sprintf("%d ", n)}, dot(sp.State), {sDim, " "}}
		}
		nameStyle := sText
		if sel || sp.Name == m.session {
			nameStyle = sBold
		}
		sb.add(line(w, hl, []seg{bar, {nameStyle, trunc(name, w-8)}}, right), i)
		sub := m.branches[sp.Path].name
		if sub == "" && sp.Path != "" {
			sub = filepath.Base(sp.Path)
		}
		sb.add(line(w, hl, []seg{{sDim, "  "}, {sBranch, trunc(sub, w-4)}}, nil), i)
	}

	// Agents of the selected space.
	sp, _ := m.space()
	var ab block
	selAgent := -1
	for i, it := range sp.Agents {
		sel := key(it.Agent) == m.agentSel
		if sel {
			selAgent = i
		}
		hl := sel && m.focus == fAgents
		bar := seg{sDim, " "}
		if sel {
			bar = seg{sAccent, "▎"}
		}
		mark := " "
		if m.marked[key(it.Agent)] {
			mark = "✓"
		}
		nameStyle := sBold
		if it.PaneID != "" && it.PaneID == m.cp.State.Borrowed {
			nameStyle = sAccent.Bold(true) // the one on screen
		}
		word := stateWord(it.State)
		ab.add(line(w, hl, []seg{bar, stateGlyph(it.State, m.frame), {sDim, mark}, {nameStyle, trunc(it.Name, w-lipgloss.Width(word.text)-6)}},
			[]seg{word, {sDim, " "}}), i)
		sub := []seg{{sDim, "    "}, {sSub, trunc(it.Tab, w/2)}}
		if a := age(it.Since); a != "" {
			sub = append(sub, seg{sDim, " · " + a})
		}
		if it.State == agents.NeedsYou && it.WaitingFor != "" {
			sub = []seg{{sDim, "    "}, {sRed, trunc(it.WaitingFor, w-5)}}
		}
		ab.add(line(w, hl, sub, nil), i)
	}
	if len(sp.Agents) == 0 {
		ab.add(line(w, false, []seg{{sDim, "  no agents here · n to start one"}}, nil), -1)
	}

	// Git panel of the selection.
	gitRoom := 0
	if h >= 26 {
		gitRoom = h * 35 / 100
	}
	gb := m.gitBlock(w)

	foot := m.footer(w)
	spaceRoom := min(len(sb.lines), max(4, h/3))
	agentRoom := h - 1 - spaceRoom - 2 - len(foot)
	if gitRoom > 0 {
		agentRoom -= gitRoom + 2
	}
	agentRoom = max(agentRoom, 2)

	var out []string
	m.clicks = m.clicks[:0]
	emit := func(s string, f focus, i int) {
		out = append(out, s)
		m.clicks = append(m.clicks, click{f, i})
	}
	emitBlock := func(b block, f focus, room int) {
		for i, l := range b.lines {
			emit(l, f, b.idx[i])
		}
		for range room - len(b.lines) {
			emit("", f, -1)
		}
	}

	c := map[agents.State]int{}
	for _, s := range m.spaces {
		for _, it := range s.Agents {
			c[it.State]++
		}
	}
	summary := []seg{}
	if c[agents.NeedsYou] > 0 {
		summary = append(summary, seg{sRed, fmt.Sprintf("! %d  ", c[agents.NeedsYou])})
	}
	summary = append(summary, seg{sYellow, fmt.Sprintf("● %d ", c[agents.Working])})
	emit(m.header("spaces", fSpaces, w, summary), fSpaces, -1)
	emitBlock(sb.fit(spaceRoom, selSpace), fSpaces, spaceRoom)
	emit("", fAgents, -1)
	emit(m.header("agents", fAgents, w, nil), fAgents, -1)
	emitBlock(ab.fit(agentRoom, selAgent), fAgents, agentRoom)
	if gitRoom > 0 {
		emit("", fGit, -1)
		emit(m.gitHeader(w), fGit, -1)
		emitBlock(gb.fit(gitRoom, m.gitSel[m.gitTab]), fGit, gitRoom)
	}
	for _, l := range foot {
		emit(l, fGit, -1)
	}
	return strings.Join(out, "\n")
}

func (m *model) gitHeader(w int) string {
	var left []seg
	for i, t := range gitTabs {
		st := sDim
		if i == m.gitTab {
			st = sDim.Bold(true)
			if m.focus == fGit {
				st = sAccent.Bold(true)
			}
		}
		left = append(left, seg{sDim, " "}, seg{st, t})
	}
	return line(w, false, left, nil)
}

func (m *model) gitBlock(w int) block {
	var b block
	dir := m.dir()
	info, have := m.git[dir]
	switch {
	case dir == "":
		b.add(line(w, false, []seg{{sDim, "  nothing selected"}}, nil), -1)
		return b
	case !have:
		b.add(line(w, false, []seg{{sDim, "  …"}}, nil), -1)
		return b
	case !info.Repo:
		b.add(line(w, false, []seg{{sDim, "  " + trunc(filepath.Base(dir)+" is not a git repo", w-3)}}, nil), -1)
		return b
	}
	focused := m.focus == fGit
	sel := m.gitSel[m.gitTab]
	num := func(f gitinfo.File) []seg {
		if f.Status == '?' {
			return nil
		}
		return []seg{{sGreen, fmt.Sprintf("+%d", f.Add)}, {sDim, "/"}, {sRed, fmt.Sprintf("-%d", f.Del)}, {sDim, " "}}
	}
	switch m.gitTab {
	case 0:
		ab := []seg{}
		if info.Ahead > 0 {
			ab = append(ab, seg{sGreen, fmt.Sprintf("↑%d", info.Ahead)})
		}
		if info.Behind > 0 {
			ab = append(ab, seg{sRed, fmt.Sprintf("↓%d", info.Behind)})
		}
		add, del, files := info.Totals()
		b.add(line(w, false, []seg{{sDim, "  "}, {sBranch, trunc(info.Branch, w-10)}}, append(ab, seg{sDim, " "})), -1)
		b.add(line(w, false, []seg{{sDim, "  "}, {sGreen, fmt.Sprintf("+%d", add)}, {sDim, "/"}, {sRed, fmt.Sprintf("-%d", del)}},
			[]seg{{sDim, fmt.Sprintf("%d files ", files)}}), -1)
		idx := 0
		section := func(title string, fs []gitinfo.File) {
			if len(fs) == 0 {
				return
			}
			b.add(line(w, false, []seg{{sSub, fmt.Sprintf("  %s (%d)", title, len(fs))}}, nil), -1)
			for _, f := range fs {
				st := sPeach
				switch f.Status {
				case 'A', '?':
					st = sGreen
				case 'D':
					st = sRed
				}
				n := num(f)
				nw := 0
				for _, s := range n {
					nw += lipgloss.Width(s.text)
				}
				b.add(line(w, focused && idx == sel, []seg{{sDim, "  "}, {st, string(f.Status)}, {sText, " " + trunc(f.Path, w-nw-6)}}, n), idx)
				idx++
			}
		}
		section("staged", info.Staged)
		section("unstaged", info.Unstaged)
		section("untracked", info.Untracked)
		if files == 0 {
			b.add(line(w, false, []seg{{sDim, "  clean"}}, nil), -1)
		}
	case 1:
		for i, c := range info.Log {
			a := age(c.When)
			b.add(line(w, focused && i == sel, []seg{{sDim, "  "}, {sPeach, c.Hash}, {sText, " " + trunc(c.Subject, w-len(c.Hash)-len(a)-6)}},
				[]seg{{sDim, a + " "}}), i)
		}
	case 2:
		for i, wt := range info.Worktrees {
			mark := seg{sDim, "  "}
			if wt.Current {
				mark = seg{sGreen, "● "}
			}
			b.add(line(w, focused && i == sel, []seg{{sDim, "  "}, mark, {sBranch, trunc(wt.Branch, w/2)}},
				[]seg{{sDim, trunc(filepath.Base(wt.Path), w/2-6) + " "}}), i)
		}
	}
	return b
}

func (m *model) footer(w int) []string {
	in := func(label string) []string {
		return []string{line(w, false, []seg{{sAccent, " " + label + " "}, {sText, m.buf}, {sAccent, "█"}}, nil)}
	}
	switch m.input {
	case filterInput:
		return in("filter")
	case promptInput:
		var names []string
		for _, a := range m.targets() {
			names = append(names, a.Name)
		}
		return []string{line(w, false, []seg{{sDim, trunc(" → "+strings.Join(names, ", "), w)}}, nil), in("prompt")[0]}
	case spawnNameInput:
		return in("new agent")
	case spawnDirInput:
		return in("in")
	}
	var hint string
	switch m.focus {
	case fSpaces:
		hint = "↑↓ space  ⏎ agents  tab next  q close"
	case fAgents:
		hint = "⏎ type  g go  p prompt  n new  ! next  / find"
	case fGit:
		hint = "←→ tab  ↑↓ select  ⏎ open  tab next"
	}
	out := []string{}
	if m.flash != "" {
		out = append(out, line(w, false, []seg{{sSub, " " + trunc(m.flash, w-2)}}, nil))
	}
	return append(out, line(w, false, []seg{{sDim, " " + trunc(hint, w-2)}}, nil))
}
