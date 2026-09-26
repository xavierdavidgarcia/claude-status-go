package cockpit

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

// fakeTmux records commands and answers list-panes with the given pane ids.
type fakeTmux struct {
	panes []string
	calls []string
}

func (f *fakeTmux) Run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if args[0] == "list-panes" {
		var lines []string
		for _, p := range f.panes {
			lines = append(lines, p+"\ts\t@1\t1\tw\t0\t/\t0\t1")
		}
		return strings.Join(lines, "\n"), nil
	}
	if args[0] == "list-windows" {
		return "@7\tshell\n@50\t" + WindowName, nil
	}
	return "", nil
}

func (f *fakeTmux) swaps() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "swap-pane") {
			out = append(out, c)
		}
	}
	return out
}

func newTest(t *testing.T, panes ...string) (*Cockpit, *fakeTmux) {
	f := &fakeTmux{panes: panes}
	c := &Cockpit{T: &tmux.Client{R: f}, Path: filepath.Join(t.TempDir(), "cockpit.json")}
	c.State = State{Slot: "%1", Sidebar: "%2"}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	return c, f
}

func TestShowSwapsBackBeforeBorrowingAnother(t *testing.T) {
	c, f := newTest(t, "%1", "%2", "%10", "%20")

	c.Show("%10", "a")
	c.Show("%10", "a") // already shown: no-op
	c.Show("%20", "b")

	want := []string{
		"swap-pane -d -s %10 -t %1",
		"swap-pane -d -s %10 -t %1", // return a
		"swap-pane -d -s %20 -t %1",
	}
	if got := f.swaps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("swaps:\n got %q\nwant %q", got, want)
	}
	if c.State.Borrowed != "%20" || c.State.BorrowedName != "b" {
		t.Fatalf("state: %+v", c.State)
	}
}

func TestShowIgnoresCockpitOwnPanes(t *testing.T) {
	c, f := newTest(t, "%1", "%2")
	c.Show("%1", "slot")
	c.Show("%2", "sidebar")
	if len(f.swaps()) != 0 {
		t.Fatalf("unexpected swaps: %q", f.swaps())
	}
}

func TestRestoreSkipsSwapWhenPaneIsGone(t *testing.T) {
	c, f := newTest(t, "%1", "%2") // borrowed %10 no longer exists
	c.State.Borrowed = "%10"
	c.save()

	if err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(f.swaps()) != 0 {
		t.Fatalf("swapped a dead pane: %q", f.swaps())
	}
	c.load()
	if c.State.Borrowed != "" {
		t.Fatalf("borrow not cleared: %+v", c.State)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	c, _ := newTest(t, "%1", "%2", "%10")
	c.Show("%10", "a")

	again := &Cockpit{T: c.T, Path: c.Path}
	again.load()
	if again.State.Borrowed != "%10" {
		t.Fatalf("restarted sidebar lost the borrow: %+v", again.State)
	}
}

func TestToggleClosesWhenLookingAtCockpit(t *testing.T) {
	c, f := newTest(t, "%1", "%2", "%10")
	c.Show("%10", "a")

	c.Toggle("@7", "") // from another window: just focus the cockpit
	if last := f.calls[len(f.calls)-1]; last != "select-window -t @50" {
		t.Fatalf("expected focus, got %q", last)
	}
	c.Toggle("@50", "%2") // from the cockpit list itself: return the agent, close
	want := []string{"swap-pane -d -s %10 -t %1", "swap-pane -d -s %10 -t %1"}
	if got := f.swaps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent not returned: %q", got)
	}
	if last := f.calls[len(f.calls)-1]; last != "kill-window -t %1" {
		t.Fatalf("cockpit not closed, last call %q", last)
	}
}

func TestToggleFromShownPaneFocusesList(t *testing.T) {
	c, f := newTest(t, "%1", "%2", "%10")
	c.Show("%10", "a")
	c.Toggle("@50", "%10") // typing in the agent: give the keyboard back
	if last := f.calls[len(f.calls)-1]; last != "select-pane -t %2" {
		t.Fatalf("expected list focus, got %q", last)
	}
}
