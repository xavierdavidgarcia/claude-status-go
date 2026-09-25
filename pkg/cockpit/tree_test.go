package cockpit

import (
	"strings"
	"testing"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

func testPanes() map[string]tmux.Pane {
	return map[string]tmux.Pane{
		"%1":  {ID: "%1", Session: "0", Window: "@1", Index: "1", WindowName: "unifi", Active: true},
		"%2":  {ID: "%2", Session: "0", Window: "@2", Index: "2", WindowName: "ephemer", Active: true},
		"%3":  {ID: "%3", Session: "0", Window: "@10", Index: "10", WindowName: "infra", Active: true},
		"%4":  {ID: "%4", Session: "0", Window: "@10", Index: "10", WindowName: "infra"},
		"%9":  {ID: "%9", Session: "1", Window: "@9", Index: "1", WindowName: "other", Active: true},
		"%50": {ID: "%50", Session: "0", Window: "@50", Index: "20", WindowName: WindowName, Active: true},
		"%51": {ID: "%51", Session: "0", Window: "@50", Index: "20", WindowName: WindowName},
	}
}

func render(rows []row) string {
	var out []string
	for _, r := range rows {
		switch r.kind {
		case sessionRow:
			out = append(out, "S:"+r.session)
		case windowRow:
			out = append(out, "W:"+r.window.Name+"/"+r.state.String())
		case agentRow:
			out = append(out, "A:"+r.agent.Name)
		}
	}
	return strings.Join(out, " ")
}

func TestTreeScopedToSession(t *testing.T) {
	as := []agents.Agent{
		{Name: "infra-a", PaneID: "%3", State: agents.Idle},
		{Name: "infra-b", PaneID: "%4", State: agents.NeedsYou},
		{Name: "eph", PaneID: "%2", State: agents.Working},
		{Name: "elsewhere", PaneID: "%9", State: agents.Working},
		{Name: "no-tmux", State: agents.Idle},
	}
	got := render(buildTree(treeInput{agents: as, panes: testPanes(), scope: "0"}))
	// Windows in index order (10 after 2), cockpit window hidden, other session
	// and non-tmux agents left out, window state = most urgent agent.
	want := "W:unifi/unknown W:ephemer/working A:eph W:infra/needs you A:infra-a A:infra-b"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestTreeAllSessions(t *testing.T) {
	as := []agents.Agent{
		{Name: "elsewhere", PaneID: "%9", State: agents.Working},
		{Name: "no-tmux", State: agents.Idle},
	}
	got := render(buildTree(treeInput{agents: as, panes: testPanes()}))
	want := "S:0 W:unifi/unknown W:ephemer/unknown W:infra/unknown S:1 W:other/working A:elsewhere S: A:no-tmux"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestTreeBorrowedAgentStaysUnderHomeWindow(t *testing.T) {
	// %2 (ephemer's agent) is in the cockpit; the slot %51 sits in @2 instead.
	panes := testPanes()
	panes["%2"] = tmux.Pane{ID: "%2", Session: "0", Window: "@50", Index: "20", WindowName: WindowName}
	panes["%51"] = tmux.Pane{ID: "%51", Session: "0", Window: "@2", Index: "2", WindowName: "ephemer", Active: true}
	as := []agents.Agent{{Name: "eph", PaneID: "%2", State: agents.Working}}

	got := render(buildTree(treeInput{agents: as, panes: panes, scope: "0", borrowed: "%2", slot: "%51"}))
	if !strings.Contains(got, "W:ephemer/working A:eph") {
		t.Fatalf("borrowed agent not under its home window: %s", got)
	}
}

func TestTreeFilterKeepsMatchingWindowsAndAgents(t *testing.T) {
	as := []agents.Agent{
		{Name: "infra-a", PaneID: "%3", State: agents.Idle},
		{Name: "eph", PaneID: "%2", State: agents.Working},
	}
	got := render(buildTree(treeInput{agents: as, panes: testPanes(), scope: "0", filter: "INFRA"}))
	if got != "W:infra/idle A:infra-a" {
		t.Fatalf("got %s", got)
	}
}
