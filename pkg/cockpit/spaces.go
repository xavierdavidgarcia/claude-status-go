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
}

func buildSpaces(in spaceInput) []space {
	byName := map[string]*space{}
	get := func(name string) *space {
		if byName[name] == nil {
			byName[name] = &space{Name: name, State: agents.Unknown}
		}
		return byName[name]
	}
	for _, p := range in.panes {
		sp := get(p.Session)
		if p.WinActive && p.Active && p.WindowName != WindowName {
			sp.ActivePane, sp.Path = p.ID, p.Path
			// The slot stands in for the pane it lent to the cockpit.
			if b, ok := in.panes[in.borrowed]; ok && p.ID == in.slot {
				sp.ActivePane, sp.Path = b.ID, b.Path
			}
		}
	}
	// A session looking at its cockpit still gets a folder for its branch line.
	for _, p := range in.panes {
		if sp := byName[p.Session]; sp.Path == "" && p.Active && p.WindowName != WindowName {
			sp.Path = p.Path
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
		// Pane ids are per tmux server: trust one only in the session the agent
		// recorded (for a borrowed pane, where its slot now stands in).
		if ok && (a.TmuxSess == "" || a.TmuxSess == p.Session) {
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
