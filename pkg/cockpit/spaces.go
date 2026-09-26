package cockpit

import (
	"sort"
	"strconv"
	"strings"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

// A space is a tmux session and the Claude agents running in it.
type space struct {
	Name       string // tmux session; "" holds agents outside tmux
	Agents     []item
	State      agents.State // most urgent agent; Unknown when none
	ActivePane string       // current pane of the session, unless it's the cockpit
	Path       string
	Tabs       []string // window names by index, cockpit excluded
}

// Label names a space: the session name, plus its first tabs when the name
// is only tmux's default number.
func (sp space) Label() string {
	if sp.Name == "" {
		return "outside tmux"
	}
	if _, err := strconv.Atoi(sp.Name); err != nil || len(sp.Tabs) == 0 {
		return sp.Name
	}
	n := min(len(sp.Tabs), 2)
	return sp.Name + " · " + strings.Join(sp.Tabs[:n], ", ")
}

type item struct {
	agents.Agent
	Tab   string // "3 Admin"
	index int
}

type spaceInput struct {
	agents   []agents.Agent
	panes    map[string]tmux.Pane
	borrowed string // pane shown in the cockpit; listed under its home tab
	slot     string // sits in the borrowed pane's home tab meanwhile
	filter   string
	// owns reports whether an agent's process runs in a pane; nil trusts the
	// recorded session.
	owns func(a agents.Agent, p tmux.Pane) bool
}

func buildSpaces(in spaceInput) []space {
	byName := map[string]*space{}
	get := func(name string) *space {
		if byName[name] == nil {
			byName[name] = &space{Name: name, State: agents.Unknown}
		}
		return byName[name]
	}
	// The slot stands in for the pane it lent to the cockpit.
	lent := func(p tmux.Pane) tmux.Pane {
		if b, ok := in.panes[in.borrowed]; ok && p.ID == in.slot {
			return tmux.Pane{ID: b.ID, Path: b.Path}
		}
		return p
	}
	for _, p := range in.panes {
		sp := get(p.Session)
		if p.WinActive && p.Active && p.WindowName != WindowName {
			q := lent(p)
			sp.ActivePane, sp.Path = q.ID, q.Path
		}
	}
	tabs := map[string]map[string]tmux.Pane{} // session → window id → a pane of it
	for _, p := range in.panes {
		if p.WindowName == WindowName {
			continue
		}
		if tabs[p.Session] == nil {
			tabs[p.Session] = map[string]tmux.Pane{}
		}
		tabs[p.Session][p.Window] = p
	}
	for name, ws := range tabs {
		var ps []tmux.Pane
		for _, p := range ws {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool {
			a, _ := strconv.Atoi(ps[i].Index)
			b, _ := strconv.Atoi(ps[j].Index)
			return a < b
		})
		for _, p := range ps {
			get(name).Tabs = append(get(name).Tabs, p.WindowName)
		}
	}
	// A session looking at its cockpit still gets a pane to preview.
	for _, p := range in.panes {
		if sp := byName[p.Session]; sp.ActivePane == "" && p.Active && p.WindowName != WindowName {
			q := lent(p)
			sp.ActivePane, sp.Path = q.ID, q.Path
		}
	}

	match := func(s string) bool {
		return in.filter == "" || strings.Contains(strings.ToLower(s), strings.ToLower(in.filter))
	}
	for _, a := range in.agents {
		home := a.PaneID
		if home != "" && home == in.borrowed {
			home = in.slot
		}
		it := item{Agent: a}
		sp := get("")
		p, ok := in.panes[home]
		// Pane ids are per tmux server: trust one only where the agent really
		// runs (for a borrowed pane, where its slot now stands in).
		if ok && in.owns != nil {
			ok = in.owns(a, in.panes[a.PaneID])
		} else if ok {
			ok = a.TmuxSess == "" || a.TmuxSess == p.Session
		}
		if ok {
			sp = get(p.Session)
			it.Tab = p.Index + " " + p.WindowName
			it.index, _ = strconv.Atoi(p.Index)
		} else {
			it.PaneID = "" // never swap a pane we can't vouch for
		}
		if !match(sp.Name + " " + it.Tab + " " + a.Name + " " + a.Cwd) {
			continue
		}
		sp.Agents = append(sp.Agents, it)
		sp.State = min(sp.State, a.State)
	}

	var out []space
	for _, sp := range byName {
		if len(sp.Agents) == 0 && (sp.Name == "" || !match(sp.Name)) {
			continue
		}
		sort.SliceStable(sp.Agents, func(i, j int) bool {
			if sp.Agents[i].index != sp.Agents[j].index {
				return sp.Agents[i].index < sp.Agents[j].index
			}
			return sp.Agents[i].Name < sp.Agents[j].Name
		})
		out = append(out, *sp)
	}
	sort.Slice(out, func(i, j int) bool { return lessSession(out[i].Name, out[j].Name) })
	return out
}

// lessSession orders numeric session names numerically, names after numbers,
// and agents outside tmux last.
func lessSession(a, b string) bool {
	if a == "" || b == "" {
		return b == "" && a != ""
	}
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return x < y
	case errA == nil:
		return true
	case errB == nil:
		return false
	}
	return a < b
}

// preview is the pane to show for a space: whoever needs you, else its first
// agent, else whatever the session is looking at.
func (sp space) preview() (pane, name string) {
	for _, it := range sp.Agents {
		if it.State == agents.NeedsYou && it.PaneID != "" {
			return it.PaneID, it.Name
		}
	}
	for _, it := range sp.Agents {
		if it.PaneID != "" {
			return it.PaneID, it.Name
		}
	}
	return sp.ActivePane, sp.Name
}
