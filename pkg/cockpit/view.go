package cockpit

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/gitinfo"
)

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

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
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

func width(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += lipgloss.Width(s.text)
	}
	return n
}

// box wraps a section in a rounded border with the title in the top edge,
// herdr-style; the border lights up when the section has focus.
func box(w int, focused bool, title, right []seg, body block, room int) block {
	bc := lipgloss.NewStyle().Foreground(cSurface1)
	if focused {
		bc = lipgloss.NewStyle().Foreground(cMauve)
	}
	render := func(segs []seg) string {
		out := ""
		for _, s := range segs {
			out += s.style.Render(s.text)
		}
		return out
	}
	fill := max(w-4-width(title)-width(right), 0)
	var b block
	b.add(bc.Render("╭─")+render(title)+bc.Render(strings.Repeat("─", fill))+render(right)+bc.Render("─╮"), -1)
	for i, l := range body.lines {
		b.add(bc.Render("│")+l+bc.Render("│"), body.idx[i])
	}
	for range room - len(body.lines) {
		b.add(bc.Render("│")+blanks(w-2)+bc.Render("│"), -1)
	}
	b.add(bc.Render("╰"+strings.Repeat("─", w-2)+"╯"), -1)
	return b
}

func (m *model) title(text string, f focus) []seg {
	st := sSub
	if m.focus == f {
		st = sAccent.Bold(true)
	}
	return []seg{{sDim, " "}, {st, text}, {sDim, " "}}
}

func (m *model) spacesBlock(iw int) (block, int) {
	var b block
	sel := 0
	for i, sp := range m.spaces {
		on := sp.Name == m.spaceSel
		if on {
			sel = i
		}
		hl := on && m.focus == fSpaces
		num := seg{sDim, fmt.Sprintf(" %d ", i+1)}
		if i >= 9 {
			num.text = "   "
		}
		if on {
			num.style = sAccent.Bold(true)
		}
		nameStyle := sText
		if on || sp.Name == m.session {
			nameStyle = sBold
		}
		var right []seg
		if sp.Name == m.session {
			right = append(right, seg{sAccent, "◆ "})
		}
		if len(sp.Agents) > 0 {
			right = append(right, dot(sp.State), seg{sDim, " "})
		}
		b.add(line(iw, hl, []seg{num, {nameStyle, trunc(sp.Label(), iw-width(right)-5)}}, right), i)
		var sub []string
		if len(sp.Tabs) > 0 {
			sub = append(sub, plural(len(sp.Tabs), "tab"))
		}
		if len(sp.Agents) > 0 {
			sub = append(sub, plural(len(sp.Agents), "agent"))
		}
		if c := needsYou(sp); c > 0 {
			sub = append(sub, fmt.Sprintf("%d need you", c))
		}
		b.add(line(iw, hl, []seg{{sDim, "   " + trunc(strings.Join(sub, " · "), iw-4)}}, nil), i)
	}
	return b, sel
}

func needsYou(sp space) int {
	n := 0
	for _, it := range sp.Agents {
		if it.State == agents.NeedsYou {
			n++
		}
	}
	return n
}

func subGlyph(s agents.Sub, frame int) seg {
	switch s.State {
	case agents.SubRunning:
		return seg{sYellow, spinner[frame%len(spinner)]}
	case agents.SubStopped:
		return seg{sDim, "✗"}
	}
	return seg{sGreen, "✓"}
}

func (m *model) agentsBlock(iw int) (block, int) {
	var b block
	sel := -1
	rows := m.rows()
	for i, r := range rows {
		on := r.key() == m.rowSel
		if on {
			sel = i
		}
		hl := on && m.focus == fAgents
		it := r.agent
		switch {
		case r.sub != nil:
			// Tree branch: └ on the agent's last line.
			branch := "├ "
			if i+1 >= len(rows) || rows[i+1].agent.PID != it.PID {
				branch = "└ "
			}
			a := age(r.sub.When)
			st := sSub
			if m.cp.State.Viewing == r.sub.Path && m.cp.State.Borrowed == "" {
				st = sAccent
			}
			b.add(line(iw, hl, []seg{{sDim, "   " + branch}, subGlyph(*r.sub, m.frame+i), {st, " " + trunc(subName(*r.sub), iw-len(a)-9)}},
				[]seg{{sDim, a + " "}}), i)
		case r.more > 0:
			b.add(line(iw, hl, []seg{{sDim, "   └ + " + plural(r.more, "more subagent")}}, nil), i)
		default:
			if i > 0 {
				b.add(line(iw, false, nil, nil), -1)
			}
			mark := seg{sDim, " "}
			if m.marked[key(it.Agent)] {
				mark = seg{sAccent, "✓"}
			}
			nameStyle := sBold
			if it.PaneID != "" && it.PaneID == m.cp.State.Borrowed {
				nameStyle = sAccent.Bold(true) // the one on screen
			}
			word := stateWord(it.State)
			b.add(line(iw, hl, []seg{{sDim, " "}, stateGlyph(it.State, m.frame), mark, {nameStyle, trunc(it.Name, iw-lipgloss.Width(word.text)-6)}},
				[]seg{word, {sDim, " "}}), i)
			sub := []seg{{sDim, "   "}, {sSub, trunc(it.Tab, iw/2)}}
			if a := age(it.Since); a != "" {
				sub = append(sub, seg{sDim, " · " + a})
			}
			if it.State == agents.NeedsYou && it.WaitingFor != "" {
				sub = []seg{{sDim, "   "}, {sRed, trunc(it.WaitingFor, iw-4)}}
			}
			b.add(line(iw, hl, sub, nil), i)
		}
	}
	if len(rows) == 0 {
		b.add(line(iw, false, []seg{{sDim, " no agents here · n to start one"}}, nil), -1)
	}
	return b, sel
}

func (m *model) View() string {
	w, h := max(m.width, 24), max(m.height, 16)
	iw := w - 2 // inside the borders

	sb, selSpace := m.spacesBlock(iw)
	ab, selRow := m.agentsBlock(iw)
	foot := m.footer(w)

	spaceRoom := min(len(sb.lines), max(4, h/4))
	gitRoom := h*30/100 - 2
	small := h < 30 // no room for all three: git replaces agents while focused
	if small {
		gitRoom = h - (spaceRoom + 2) - len(foot) - 2
	}
	agentRoom := h - (spaceRoom + 2) - len(foot) - 2
	if !small {
		agentRoom -= gitRoom + 2
	}
	agentRoom = max(agentRoom, 2)

	c := map[agents.State]int{}
	for _, s := range m.spaces {
		for _, it := range s.Agents {
			c[it.State]++
		}
	}
	var sum []seg
	if c[agents.NeedsYou] > 0 {
		sum = append(sum, seg{sRed, fmt.Sprintf(" ! %d", c[agents.NeedsYou])})
	}
	sum = append(sum, seg{sYellow, fmt.Sprintf(" ● %d ", c[agents.Working])})

	sp, _ := m.space()
	var out []string
	m.clicks = m.clicks[:0]
	emit := func(b block, f focus) {
		for i, l := range b.lines {
			out = append(out, l)
			m.clicks = append(m.clicks, click{f, b.idx[i]})
		}
	}
	emit(box(w, m.focus == fSpaces, m.title("spaces", fSpaces), sum, sb.fit(spaceRoom, selSpace), spaceRoom), fSpaces)
	if !small || m.focus != fGit {
		emit(box(w, m.focus == fAgents, m.title("agents", fAgents), []seg{{sDim, " " + trunc(sp.Label(), w/2) + " "}}, ab.fit(agentRoom, selRow), agentRoom), fAgents)
	}
	m.gitTitleY = -1
	if !small || m.focus == fGit {
		m.gitTitleY = len(out)
		emit(box(w, m.focus == fGit, m.gitTitle(), nil, m.gitBlock(iw).fit(gitRoom, m.gitSel[m.gitTab]), gitRoom), fGit)
	}
	for _, l := range foot {
		out = append(out, l)
		m.clicks = append(m.clicks, click{fGit, -1})
	}
	return strings.Join(out, "\n")
}

// gitTitle renders the tab names in the git box's edge and records where
// each one is, so a click can switch to it.
func (m *model) gitTitle() []seg {
	out := []seg{{sDim, " "}}
	x := 2 + 1 // "╭─" then the leading space
	m.gitTabsAt = m.gitTabsAt[:0]
	for i, t := range gitTabs {
		if i > 0 {
			out = append(out, seg{sDim, " · "})
			x += 3
		}
		st := sDim
		if i == m.gitTab {
			st = sSub.Bold(true)
			if m.focus == fGit {
				st = sAccent.Bold(true)
			}
		}
		out = append(out, seg{st, t})
		m.gitTabsAt = append(m.gitTabsAt, [2]int{x, x + len(t)})
		x += len(t)
	}
	return append(out, seg{sDim, " "})
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
		var ab []seg
		if info.Ahead > 0 {
			ab = append(ab, seg{sGreen, fmt.Sprintf("↑%d", info.Ahead)})
		}
		if info.Behind > 0 {
			ab = append(ab, seg{sRed, fmt.Sprintf("↓%d", info.Behind)})
		}
		add, del, files := info.Totals()
		b.add(line(w, false, []seg{{sDim, "  "}, {sBranch, trunc(info.Branch, w-10)}}, append(ab, seg{sDim, " "})), -1)
		b.add(line(w, false, []seg{{sDim, "  "}, {sGreen, fmt.Sprintf("+%d", add)}, {sDim, "/"}, {sRed, fmt.Sprintf("-%d", del)}},
			[]seg{{sDim, plural(files, "file") + " "}}), -1)
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
				b.add(line(w, focused && idx == sel, []seg{{sDim, "  "}, {st, string(f.Status)}, {sText, " " + trunc(f.Path, w-width(n)-6)}}, n), idx)
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
		if len(info.Log) == 0 {
			b.add(line(w, false, []seg{{sDim, "  no commits yet"}}, nil), -1)
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
	case renameInput:
		return in("rename space")
	}
	var hint string
	switch m.focus {
	case fSpaces:
		hint = "⏎ switch  r rename  ↑↓ move  1-9"
	case fAgents:
		hint = "⏎ show  → type  p prompt  n new  ! next"
	case fGit:
		hint = "←→ tab  ↑↓ select  ⏎ open"
	}
	var out []string
	if m.flash != "" {
		out = append(out, line(w, false, []seg{{sSub, " " + trunc(m.flash, w-2)}}, nil))
	}
	return append(out, line(w, false, []seg{{sDim, " " + trunc(hint, w-2)}}, nil))
}
