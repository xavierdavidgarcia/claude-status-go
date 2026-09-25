package cockpit

import (
	"strings"
	"testing"

	"github.com/xgarcia/claude-status-go/pkg/agents"
	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

func testPanes() map[string]tmux.Pane {
	return map[string]tmux.Pane{
		"%1":  {ID: "%1", Session: "0", Window: "@1", Index: "1", WindowName: "unifi", Active: true, WinActive: true, Path: "/u"},
		"%2":  {ID: "%2", Session: "0", Window: "@2", Index: "2", WindowName: "ephemer", Active: true},
		"%3":  {ID: "%3", Session: "0", Window: "@10", Index: "10", WindowName: "infra", Active: true},
		"%9":  {ID: "%9", Session: "10", Window: "@9", Index: "1", WindowName: "other", Active: true},
		"%8":  {ID: "%8", Session: "work", Window: "@8", Index: "1", WindowName: "w", Active: true},
		"%7":  {ID: "%7", Session: "2", Window: "@7", Index: "1", WindowName: WindowName, Active: true, WinActive: true},
		"%50": {ID: "%50", Session: "0", Window: "@50", Index: "20", WindowName: WindowName, Active: true},
	}
}

func summary(sps []space) string {
	var out []string
	for _, sp := range sps {
		s := "[" + sp.Name + " " + sp.State.String()
		for _, it := range sp.Agents {
			s += " " + it.Name + "@" + it.Tab
		}
		out = append(out, s+"]")
	}
	return strings.Join(out, " ")
}

func TestSpaces(t *testing.T) {
	as := []agents.Agent{
		{Name: "infra", PaneID: "%3", State: agents.Idle},
		{Name: "eph", PaneID: "%2", State: agents.NeedsYou},
		{Name: "other", PaneID: "%9", State: agents.Working},
		{Name: "loose", State: agents.Idle},
	}
	got := summary(buildSpaces(spaceInput{agents: as, panes: testPanes()}))
	// Numeric sessions in numeric order, named after, outside-tmux last; agents
	// ordered by tab index; a space's state is its most urgent agent's.
	want := "[0 needs you eph@2 ephemer infra@10 infra] [2 unknown] [10 working other@1 other] [work unknown] [ idle loose@]"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestSpaceActivePaneSkipsCockpit(t *testing.T) {
	sps := buildSpaces(spaceInput{panes: testPanes()})
	for _, sp := range sps {
		if sp.Name == "0" && (sp.ActivePane != "%1" || sp.Path != "/u") {
			t.Errorf("session 0: %+v", sp)
		}
		if sp.Name == "2" && sp.ActivePane != "" {
			t.Errorf("cockpit window must not be a preview target: %+v", sp)
		}
	}
}

func TestBorrowedAgentStaysUnderHomeTab(t *testing.T) {
	panes := testPanes()
	// %2 is in the cockpit; the slot %51 sits in its home tab @2.
	panes["%2"] = tmux.Pane{ID: "%2", Session: "0", Window: "@50", Index: "20", WindowName: WindowName}
	panes["%51"] = tmux.Pane{ID: "%51", Session: "0", Window: "@2", Index: "2", WindowName: "ephemer"}
	as := []agents.Agent{
		{Name: "eph", PaneID: "%2", TmuxSess: "0", State: agents.Working},
		{Name: "twin", PaneID: "%2", TmuxSess: "other-server", State: agents.Idle}, // same id, other server
	}
	got := summary(buildSpaces(spaceInput{agents: as, panes: panes, borrowed: "%2", slot: "%51"}))
	if !strings.Contains(got, "[0 working eph@2 ephemer") || !strings.Contains(got, "[ idle twin@]") {
		t.Fatalf("got %s", got)
	}
}

func TestPreviewPrefersWhoNeedsYou(t *testing.T) {
	sp := space{ActivePane: "%1", Agents: []item{
		{Agent: agents.Agent{Name: "a", PaneID: "%2", State: agents.Working}},
		{Agent: agents.Agent{Name: "b", PaneID: "%3", State: agents.NeedsYou}},
	}}
	if p, _ := sp.preview(); p != "%3" {
		t.Fatalf("got %s", p)
	}
	if p, _ := (space{ActivePane: "%1"}).preview(); p != "%1" {
		t.Fatalf("empty space should preview its active pane, got %s", p)
	}
}

func TestFilter(t *testing.T) {
	as := []agents.Agent{
		{Name: "infra", PaneID: "%3", State: agents.Idle},
		{Name: "eph", PaneID: "%2", State: agents.Working},
	}
	got := summary(buildSpaces(spaceInput{agents: as, panes: testPanes(), filter: "INFRA"}))
	if got != "[0 idle infra@10 infra]" {
		t.Fatalf("got %s", got)
	}
}

func TestForeignPaneIDIsNotTrusted(t *testing.T) {
	// %3 exists here but the agent recorded another session (another server).
	as := []agents.Agent{{Name: "far", PaneID: "%3", TmuxSess: "elsewhere", State: agents.Idle}}
	sps := buildSpaces(spaceInput{agents: as, panes: testPanes()})
	last := sps[len(sps)-1]
	if last.Name != "" || last.Agents[0].PaneID != "" {
		t.Fatalf("foreign pane should be outside tmux and unswappable: %+v", last)
	}
}
