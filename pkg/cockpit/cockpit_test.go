package cockpit

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

// fakeTmux keeps panes, windows and pane options, and moves them the way tmux
// does, so cockpit operations can be checked by where panes end up.
type fakeTmux struct {
	panes map[string]*fpane
	wins  map[string]*fwin
	next  int
	calls []string
}

type fpane struct {
	id, win string
	opts    map[string]string
}

type fwin struct{ id, name, session string }

func newFake() *fakeTmux {
	return &fakeTmux{panes: map[string]*fpane{}, wins: map[string]*fwin{}, next: 100}
}

// window adds a window with one pane and returns the pane id.
func (f *fakeTmux) window(session, name string) string {
	f.next++
	w := &fwin{id: fmt.Sprintf("@%d", f.next), name: name, session: session}
	f.wins[w.id] = w
	f.next++
	p := &fpane{id: fmt.Sprintf("%%%d", f.next), win: w.id, opts: map[string]string{}}
	f.panes[p.id] = p
	return p.id
}

func arg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeTmux) winOf(target string) *fwin {
	if p, ok := f.panes[target]; ok {
		return f.wins[p.win]
	}
	return f.wins[target]
}

func (f *fakeTmux) Run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "list-panes":
		format := arg(args, "-F")
		var ids []string
		for id := range f.panes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var lines []string
		for _, id := range ids {
			p := f.panes[id]
			w := f.wins[p.win]
			var fields []string
			for _, tok := range strings.Split(format, "\t") {
				tok = strings.TrimSuffix(strings.TrimPrefix(tok, "#{"), "}")
				switch {
				case tok == "pane_id":
					fields = append(fields, p.id)
				case tok == "window_id":
					fields = append(fields, w.id)
				case tok == "window_name":
					fields = append(fields, w.name)
				case tok == "session_name":
					fields = append(fields, w.session)
				case strings.HasPrefix(tok, "@"):
					fields = append(fields, p.opts[tok])
				default:
					fields = append(fields, "0")
				}
			}
			lines = append(lines, strings.Join(fields, "\t"))
		}
		return strings.Join(lines, "\n"), nil
	case "swap-pane":
		a, b := f.panes[arg(args, "-s")], f.panes[arg(args, "-t")]
		if a == nil || b == nil {
			return "", fmt.Errorf("can't find pane")
		}
		a.win, b.win = b.win, a.win
	case "set-option":
		if args[1] == "-p" {
			p := f.panes[arg(args, "-t")]
			if p == nil {
				return "", fmt.Errorf("no pane")
			}
			if args[2] == "-u" {
				delete(p.opts, args[len(args)-1])
			} else {
				p.opts[args[len(args)-2]] = args[len(args)-1]
			}
		}
	case "new-window":
		return f.window(strings.TrimSuffix(arg(args, "-t"), ":"), arg(args, "-n")), nil
	case "split-window":
		t := f.panes[arg(args, "-t")]
		f.next++
		p := &fpane{id: fmt.Sprintf("%%%d", f.next), win: t.win, opts: map[string]string{}}
		f.panes[p.id] = p
		return p.id, nil
	case "kill-window":
		w := f.winOf(arg(args, "-t"))
		for id, p := range f.panes {
			if p.win == w.id {
				delete(f.panes, id)
			}
		}
		delete(f.wins, w.id)
	case "break-pane":
		p := f.panes[arg(args, "-s")]
		f.next++
		w := &fwin{id: fmt.Sprintf("@%d", f.next), name: arg(args, "-n"), session: strings.TrimSuffix(arg(args, "-t"), ":")}
		f.wins[w.id] = w
		p.win = w.id
	case "move-window":
		f.winOf(arg(args, "-s")).session = strings.TrimSuffix(arg(args, "-t"), ":")
	}
	return "", nil
}

// where says which window (by name) a pane is in; "" when it's gone.
func (f *fakeTmux) where(pane string) string {
	if p, ok := f.panes[pane]; ok {
		return f.wins[p.win].name
	}
	return ""
}

func (f *fakeTmux) last() string { return f.calls[len(f.calls)-1] }

func setup(t *testing.T) (*fakeTmux, *Cockpit, string, string) {
	f := newFake()
	a := f.window("0", "admin")
	b := f.window("0", "infra")
	c := &Cockpit{T: &tmux.Client{R: f}, Session: "0"}
	if err := c.Toggle("", ""); err != nil {
		t.Fatal(err)
	}
	if c.State.Window == "" {
		t.Fatal("no cockpit created")
	}
	return f, c, a, b
}

func TestShowAndRestoreByPaneOptions(t *testing.T) {
	f, c, a, b := setup(t)

	c.Show(a, "admin-agent")
	if f.where(a) != WindowName || f.where(c.State.Slot) != "admin" {
		t.Fatalf("a not shown: a in %q, slot in %q", f.where(a), f.where(c.State.Slot))
	}
	c.Show(b, "infra-agent") // a goes home first
	if f.where(a) != "admin" || f.where(b) != WindowName || f.where(c.State.Slot) != "infra" {
		t.Fatalf("a=%s b=%s slot=%s", f.where(a), f.where(b), f.where(c.State.Slot))
	}

	// A fresh process (restarted sidebar) finds the same state in tmux.
	again := New(c.T, "0")
	if again.State.Borrowed != b || again.State.Name != "infra-agent" {
		t.Fatalf("state not recovered from pane options: %+v", again.State)
	}
	again.Restore()
	if f.where(b) != "infra" || f.panes[b].opts[optHome] != "" {
		t.Fatalf("b not home or still tagged: %s %v", f.where(b), f.panes[b].opts)
	}
}

func TestToggle(t *testing.T) {
	f, c, a, _ := setup(t)
	c.Show(a, "admin-agent")
	win, sidebar := c.State.Window, c.State.Sidebar

	c.Toggle(win, a) // typing in the agent: give the keyboard back
	if f.last() != "select-pane -t "+sidebar {
		t.Fatalf("expected list focus, got %q", f.last())
	}
	c.Toggle(win, sidebar) // from the list: close, agent home
	if f.where(a) != "admin" || f.wins[win] != nil {
		t.Fatalf("a in %q, cockpit still there: %v", f.where(a), f.wins[win] != nil)
	}
}

func TestToggleFromAnotherSessionMovesTheCockpit(t *testing.T) {
	f, c, _, _ := setup(t)
	other := New(c.T, "work")
	other.Toggle("@elsewhere", "")
	if len(other.layouts()) != 1 || f.wins[other.State.Window].session != "work" {
		t.Fatalf("expected the one cockpit moved to work, got %+v", other.layouts())
	}
}

// The state earlier versions left behind: two cockpits, one holding an agent
// whose slot (and home tab) is gone.
func TestDuplicatesAreCleanedWithoutKillingAgents(t *testing.T) {
	f, c, a, b := setup(t)
	c.Show(a, "admin-agent")

	slot := f.window("2", WindowName)
	sidebar := f.window("2", "x")
	f.panes[sidebar].win = f.panes[slot].win
	f.panes[slot].opts[optRole] = "slot"
	f.panes[sidebar].opts[optRole] = "sidebar"
	f.panes[sidebar].opts[optSlot] = slot
	// b was borrowed into that second cockpit, then its slot died with b's tab.
	f.panes[b].win = f.panes[sidebar].win
	f.panes[b].opts[optHome] = slot
	f.panes[b].opts[optName] = "infra-agent"
	delete(f.panes, slot)

	c.Toggle("", "")
	if n := len(c.layouts()); n != 1 {
		t.Fatalf("want one cockpit left, got %d", n)
	}
	if w := f.where(b); w != "infra-agent" {
		t.Fatalf("b should be rescued into its own tab, is in %q", w)
	}
	if f.where(a) != WindowName {
		t.Fatalf("the kept cockpit lost its agent: a in %q", f.where(a))
	}
}

func TestRestoreWhenSlotWasClosed(t *testing.T) {
	f, c, a, _ := setup(t)
	c.Show(a, "admin-agent")
	delete(f.panes, c.State.Slot) // someone closed the placeholder in a's tab
	if err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	if f.where(a) != "admin-agent" {
		t.Fatalf("a should get its own tab, is in %q", f.where(a))
	}
}
