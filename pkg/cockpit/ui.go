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
	renameInput
)

const (
	gitTTL    = 5 * time.Second
	showDelay = 120 * time.Millisecond // let the cursor settle before swapping panes
	subsShown = 3                      // finished subagents listed before "+ N more"
)

var gitTabs = []string{"changes", "log", "worktrees"}

type (
	scanMsg struct {
		agents []agents.Agent
		panes  map[string]tmux.Pane
		subs   map[int][]agents.Sub // by agent pid
	}
	gitMsg  gitinfo.Info
	showMsg struct{ target target }
	tickMsg struct{}
	spinMsg struct{}
)

// target is what the right side shows: a pane, or a subagent's transcript.
type target struct {
	pane, path, name string
}

// arow is a line of the agents list: an agent, one of its subagents, or the
// "+ N more" row folding older subagents.
type arow struct {
	agent item
	sub   *agents.Sub
	more  int
}

func (r arow) key() string {
	switch {
	case r.sub != nil:
		return "sub:" + r.sub.Path
	case r.more > 0:
		return "more:" + key(r.agent.Agent)
	}
	return key(r.agent.Agent)
}

type model struct {
	session string // tmux session the cockpit lives in
	scanner *agents.Scanner
	t       *tmux.Client
	cp      *Cockpit

	agents []agents.Agent
	panes  map[string]tmux.Pane
	subs   map[int][]agents.Sub
	spaces []space

	focus    focus
	spaceSel string // space name
	rowSel   string // arow key
	expanded map[string]bool
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
	ownCache map[[2]int]bool

	width, height int
	frame         int
	clicks        []click  // what each screen line selects
	gitTabsAt     [][2]int // x ranges of the git tab titles
	gitTitleY     int
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
		spaceSel: session, focus: fAgents, marked: map[string]bool{}, expanded: map[string]bool{},
		git: map[string]gitinfo.Info{}, loading: map[string]bool{}, ownCache: map[[2]int]bool{}, gitTitleY: -1}
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

func (m *model) scan() scanMsg {
	as := m.scanner.Scan()
	subs := map[int][]agents.Sub{}
	now := time.Now()
	for _, a := range as {
		if ss := m.scanner.Subagents(a, now); len(ss) > 0 {
			subs[a.PID] = ss
		}
	}
	return scanMsg{agents: as, panes: m.t.Panes(), subs: subs}
}

func key(a agents.Agent) string {
	if a.PaneID != "" {
		return a.PaneID
	}
	return fmt.Sprintf("pid:%d", a.PID)
}

func (m *model) refresh() { m.apply(scanMsg{agents: m.agents, panes: m.panes}) }

func (m *model) apply(s scanMsg) {
	m.agents, m.panes = s.agents, s.panes
	if s.subs != nil {
		m.subs = s.subs
	}
	m.cp.load()
	m.spaces = buildSpaces(spaceInput{agents: m.agents, panes: m.panes,
		borrowed: m.cp.State.Borrowed, slot: m.cp.State.Slot, filter: m.filter, owns: m.owns})
	if _, ok := m.space(); !ok && len(m.spaces) > 0 {
		m.spaceSel = m.spaces[0].Name
	}
	rows := m.rows()
	for _, r := range rows {
		if r.key() == m.rowSel {
			return
		}
	}
	m.rowSel = ""
	if len(rows) > 0 {
		m.rowSel = rows[0].key()
	}
}

// owns ties an agent to a pane by process ancestry, cached per pair.
func (m *model) owns(a agents.Agent, p tmux.Pane) bool {
	if p.PID == 0 {
		return false
	}
	k := [2]int{a.PID, p.PID}
	v, ok := m.ownCache[k]
	if !ok {
		v = m.scanner.Descends(a.PID, p.PID)
		m.ownCache[k] = v
	}
	return v
}

func (m *model) space() (space, bool) {
	for _, sp := range m.spaces {
		if sp.Name == m.spaceSel {
			return sp, true
		}
	}
	return space{}, false
}

// rows lists the selected space's agents, each followed by its running
// subagents and the latest finished ones.
func (m *model) rows() []arow {
	sp, _ := m.space()
	var out []arow
	for _, it := range sp.Agents {
		out = append(out, arow{agent: it})
		subs := m.subs[it.PID]
		shown := 0
		for i := range subs {
			if subs[i].State == agents.SubRunning || shown < subsShown || m.expanded[key(it.Agent)] {
				out = append(out, arow{agent: it, sub: &subs[i]})
				shown++
			}
		}
		if hidden := len(subs) - shown; hidden > 0 {
			out = append(out, arow{agent: it, more: hidden})
		}
	}
	return out
}

func (m *model) row() (arow, int, bool) {
	for i, r := range m.rows() {
		if r.key() == m.rowSel {
			return r, i, true
		}
	}
	return arow{}, -1, false
}

func (m *model) agent() (item, bool) {
	r, _, ok := m.row()
	return r.agent, ok
}

// dir is what the git panel describes: the agent's repo, else the space's.
func (m *model) dir() string {
	if it, ok := m.agent(); ok {
		return it.Cwd
	}
	sp, _ := m.space()
	return sp.Path
}

// ── background work ─────────────────────────────────────

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

// selected is what the right side should show for the cursor.
func (m *model) selected() target {
	if m.focus == fSpaces {
		sp, _ := m.space()
		p, n := sp.preview()
		return target{pane: p, name: n}
	}
	r, _, ok := m.row()
	switch {
	case !ok:
		return target{}
	case r.sub != nil:
		return target{path: r.sub.Path, name: r.agent.Name + " › " + subName(*r.sub)}
	}
	return target{pane: r.agent.PaneID, name: r.agent.Name}
}

func subName(s agents.Sub) string {
	if s.Desc != "" {
		return s.Desc
	}
	return s.Type
}

func (m *model) showing(t target) bool {
	if t.path != "" {
		return m.cp.State.Viewing == t.path && m.cp.State.Borrowed == ""
	}
	return t.pane == m.cp.State.Borrowed
}

// follow shows the selection once the cursor has settled.
func (m *model) follow() tea.Cmd {
	t := m.selected()
	if (t.pane == "" && t.path == "") || m.showing(t) {
		return nil
	}
	return tea.Tick(showDelay, func(time.Time) tea.Msg { return showMsg{t} })
}

func (m *model) show(t target) {
	var err error
	switch {
	case t.path != "":
		err = m.cp.View(t.path, t.name)
	case t.pane != "":
		err = m.cp.Show(t.pane, t.name)
	}
	if err != nil {
		m.flash = err.Error()
	}
}

// ── update ──────────────────────────────────────────────

func (m *model) Init() tea.Cmd { return tea.Batch(tick(), spin(), m.loadGit(), m.follow()) }

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
		return m, m.loadGit()
	case gitMsg:
		m.git[msg.Dir] = gitinfo.Info(msg)
		delete(m.loading, msg.Dir)
	case showMsg:
		if msg.target == m.selected() { // still selected
			m.show(msg.target)
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
	if msg.Y == m.gitTitleY {
		for i, r := range m.gitTabsAt {
			if msg.X >= r[0] && msg.X < r[1] {
				m.focus, m.gitTab = fGit, i
			}
		}
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
		m.refresh()
		m.enterSpace()
	case fAgents:
		if rows := m.rows(); c.index < len(rows) {
			m.rowSel = rows[c.index].key()
			return tea.Batch(m.activate(), m.loadGit())
		}
	case fGit:
		m.gitSel[m.gitTab] = c.index
		return m.openGitItem()
	}
	return tea.Batch(m.follow(), m.loadGit())
}

// move steps the cursor, flowing from one section into the next at its edges.
func (m *model) move(d int) tea.Cmd {
	switch m.focus {
	case fSpaces:
		i := m.spaceIndex()
		switch j := i + d; {
		case j >= len(m.spaces):
			m.focus = fAgents
			if rows := m.rows(); len(rows) > 0 {
				m.rowSel = rows[0].key()
			}
		case j >= 0:
			m.spaceSel, m.rowSel = m.spaces[j].Name, ""
			m.refresh()
		}
	case fAgents:
		rows := m.rows()
		_, i, _ := m.row()
		switch j := i + d; {
		case j < 0:
			m.focus = fSpaces
		case j >= len(rows):
			if m.height >= 30 {
				m.focus = fGit
			}
		default:
			m.rowSel = rows[j].key()
		}
	case fGit:
		n := len(m.gitItems())
		if j := m.gitSel[m.gitTab] + d; j < 0 {
			m.focus = fAgents
		} else {
			m.gitSel[m.gitTab] = min(j, max(n-1, 0))
		}
		return nil
	}
	return tea.Batch(m.follow(), m.loadGit())
}

func (m *model) spaceIndex() int {
	for i, sp := range m.spaces {
		if sp.Name == m.spaceSel {
			return i
		}
	}
	return 0
}

func (m *model) key(k string) (tea.Model, tea.Cmd) {
	m.flash = ""
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.refresh()
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
		switch m.focus {
		case fGit:
			m.gitTab = (m.gitTab + 2) % 3
		case fAgents:
			m.focus = fSpaces
		}
	case "right", "l", "i":
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
			m.enterSpace()
			m.focus = fAgents
			return m, m.follow()
		case fAgents:
			return m, m.activate()
		case fGit:
			return m, m.openGitItem()
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(k[0] - '1'); i < len(m.spaces) {
			m.spaceSel, m.rowSel, m.focus = m.spaces[i].Name, "", fSpaces
			m.refresh()
			return m, tea.Batch(m.follow(), m.loadGit())
		}
	case "g":
		if t := m.selected(); t.pane != "" {
			m.cp.Restore()
			m.t.Jump(t.pane)
		}
	case "r":
		if sp, ok := m.space(); ok && sp.Name != "" {
			m.input, m.buf = renameInput, sp.Name
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

// activate is Enter on an agents row: show it now; a "+ N more" row unfolds.
func (m *model) activate() tea.Cmd {
	r, _, ok := m.row()
	if !ok {
		return nil
	}
	if r.more > 0 {
		m.expanded[key(r.agent.Agent)] = true
		return nil
	}
	m.show(m.selected())
	return nil
}

// enterSpace switches the client to the selected space, taking the cockpit along.
func (m *model) enterSpace() {
	if m.spaceSel == "" || m.spaceSel == m.session {
		return
	}
	if err := m.cp.MoveTo(m.spaceSel); err != nil {
		m.flash = "switch: " + err.Error()
	}
	m.session = m.cp.Session // moved even if the client couldn't follow
}

// typeInto puts the keyboard in the agent's pane; prefix+g brings it back.
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
	selAgent := ""
	if it, ok := m.agent(); ok {
		selAgent = key(it.Agent)
	}
	for _, sp := range m.spaces {
		for _, it := range sp.Agents {
			here := sp.Name == m.spaceSel && key(it.Agent) == selAgent
			if here {
				cur = len(all)
			}
			if it.State == agents.NeedsYou || here {
				all = append(all, pos{sp.Name, key(it.Agent)})
			}
		}
	}
	for i := 1; i <= len(all); i++ {
		p := all[(max(cur, 0)+i)%len(all)]
		if p.sp == m.spaceSel && p.ag == selAgent {
			continue
		}
		m.spaceSel, m.rowSel, m.focus = p.sp, p.ag, fAgents
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
	m.refresh()
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
	case renameInput:
		m.rename(buf)
	}
	return m, nil
}

func (m *model) rename(name string) {
	old := m.spaceSel
	if name == "" || name == old {
		return
	}
	if err := m.t.RenameSession(old, name); err != nil {
		m.flash = "rename: " + err.Error()
		return
	}
	if old == m.session {
		m.cp.Session = name
		m.session = name
	}
	m.spaceSel = name
	m.apply(m.scan())
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
	space := m.spaceSel
	if space == "" {
		space = m.session
	}
	pane, err := m.t.NewWindow(space+":", name, dir, cmd)
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
					m.spaceSel, m.rowSel, m.focus = sp.Name, key(a.Agent), fAgents
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
	if err := m.t.Popup(m.session, dir, cmd); err != nil {
		m.flash = "popup: " + err.Error()
	}
}
