package cockpit

import (
	"sort"
	"strconv"
	"strings"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

type rowKind int

const (
	sessionRow rowKind = iota
	windowRow
	agentRow
)

// row is one line of the tree: session (only when showing all sessions) →
// window ("space") → agent.
type row struct {
	kind    rowKind
	key     string
	session string
	window  window
	agent   agents.Agent
	state   agents.State // agent state, or the most urgent one below a window
	agentsN int
}

type window struct {
	ID, Index, Name, Session string
	ActivePane, Path         string
}

type treeInput struct {
	agents   []agents.Agent
	panes    map[string]tmux.Pane
	scope    string // session name; "" shows every session
	borrowed string // pane shown in the cockpit, listed under its home window
	slot     string // where the borrowed pane's home window now is
	filter   string
}

func buildTree(in treeInput) []row {
	windows := map[string]*window{}
	byWindow := map[string][]agents.Agent{}
	for _, p := range in.panes {
		if p.WindowName == WindowName || (in.scope != "" && p.Session != in.scope) {
			continue
		}
		w := windows[p.Window]
		if w == nil {
			w = &window{ID: p.Window, Index: p.Index, Name: p.WindowName, Session: p.Session}
			windows[p.Window] = w
		}
		if p.Active {
			w.ActivePane, w.Path = p.ID, p.Path
		}
	}
	var loose []agents.Agent // not in tmux
	for _, a := range in.agents {
		home := a.PaneID
		if home != "" && home == in.borrowed {
			home = in.slot
		}
		p, ok := in.panes[home]
		switch {
		case ok && windows[p.Window] != nil:
			byWindow[p.Window] = append(byWindow[p.Window], a)
		case !ok && in.scope == "":
			loose = append(loose, a)
		}
	}

	var ws []*window
	for _, w := range windows {
		ws = append(ws, w)
	}
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].Session != ws[j].Session {
			return ws[i].Session < ws[j].Session
		}
		a, _ := strconv.Atoi(ws[i].Index)
		b, _ := strconv.Atoi(ws[j].Index)
		return a < b
	})

	match := func(s string) bool {
		return in.filter == "" || strings.Contains(strings.ToLower(s), strings.ToLower(in.filter))
	}
	var rows []row
	lastSession := ""
	for _, w := range ws {
		var kids []row
		state := agents.Unknown
		for _, a := range byWindow[w.ID] {
			if match(w.Name) || match(a.Name+" "+a.Cwd) {
				kids = append(kids, row{kind: agentRow, key: key(a), session: w.Session, window: *w, agent: a, state: a.State})
				state = min(state, a.State)
			}
		}
		if len(kids) == 0 && !match(w.Name) {
			continue
		}
		if in.scope == "" && w.Session != lastSession {
			lastSession = w.Session
			rows = append(rows, row{kind: sessionRow, key: "s:" + w.Session, session: w.Session})
		}
		rows = append(rows, row{kind: windowRow, key: "w:" + w.ID, session: w.Session, window: *w, state: state, agentsN: len(kids)})
		rows = append(rows, kids...)
	}
	if len(loose) > 0 {
		rows = append(rows, row{kind: sessionRow, key: "s:", session: ""})
		for _, a := range loose {
			if match(a.Name + " " + a.Cwd) {
				rows = append(rows, row{kind: agentRow, key: key(a), agent: a, state: a.State})
			}
		}
	}
	return rows
}
