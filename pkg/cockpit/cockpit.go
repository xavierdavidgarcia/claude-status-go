// Package cockpit implements the tmux side of the agent cockpit: a window with
// the agent list on the left and a slot on the right that an agent's real
// pane is swapped into.
//
// There's one cockpit per tmux server. Nothing is kept on disk: the panes carry
// their roles as tmux pane options, which travel with them through swaps, so
// the pairing between a borrowed pane and the slot holding its place can't go
// stale.
package cockpit

import (
	"fmt"
	"os"
	"strings"

	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

const (
	WindowName = "cockpit"

	optRole = "@cockpit_role" // "sidebar" or "slot"
	optSlot = "@cockpit_slot" // on the sidebar: its slot pane
	optHome = "@cockpit_home" // on a borrowed pane: the slot standing in its tab
	optView = "@cockpit_view" // on the slot: transcript it shows
	optName = "@cockpit_title"
)

// Layout is what tmux says the cockpit looks like right now.
type Layout struct {
	Window   string // cockpit window id; "" when there's no cockpit
	Session  string
	Sidebar  string
	Slot     string
	Borrowed string // pane lent to the cockpit, if any
	Name     string // its title
	Viewing  string // transcript path shown in the slot, if any
	Dead     bool   // the sidebar's program exited (crash, kill)
}

type paneInfo struct {
	id, window, windowName, session, role, slot, home, view, title, dead string
}

type Cockpit struct {
	T       *tmux.Client
	Session string // session the cockpit is used from
	State   Layout
}

func New(t *tmux.Client, session string) *Cockpit {
	c := &Cockpit{T: t, Session: session}
	c.load()
	return c
}

// CurrentSession is the session of the pane or client we run in.
func CurrentSession(t *tmux.Client) (string, error) {
	s, err := t.Display(os.Getenv("TMUX_PANE"), "#{session_name}")
	if err != nil {
		return "", fmt.Errorf("not inside tmux: %w", err)
	}
	return s, nil
}

func (c *Cockpit) panes() []paneInfo {
	out, err := c.T.R.Run("list-panes", "-a", "-F", strings.Join([]string{
		"#{pane_id}", "#{window_id}", "#{window_name}", "#{session_name}",
		"#{" + optRole + "}", "#{" + optSlot + "}", "#{" + optHome + "}", "#{" + optView + "}", "#{" + optName + "}",
		"#{pane_dead}",
	}, "\t"))
	if err != nil {
		return nil
	}
	var ps []paneInfo
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 10 {
			ps = append(ps, paneInfo{f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8], f[9]})
		}
	}
	return ps
}

// layouts reads every cockpit on the server from the panes' options.
func (c *Cockpit) layouts() []Layout {
	ps := c.panes()
	var ls []Layout
	for _, p := range ps {
		if p.role != "sidebar" {
			continue
		}
		l := Layout{Window: p.window, Session: p.session, Sidebar: p.id, Slot: p.slot, Dead: p.dead == "1"}
		for _, q := range ps {
			switch {
			case q.id == l.Slot:
				l.Viewing = q.view
			case q.home != "" && q.home == l.Slot && q.window == l.Window:
				l.Borrowed, l.Name = q.id, q.title
			}
		}
		ls = append(ls, l)
	}
	return ls
}

// load refreshes State with the cockpit tmux currently shows.
func (c *Cockpit) load() {
	c.State = Layout{}
	for _, l := range c.layouts() {
		if c.State.Window == "" || l.Session == c.Session {
			c.State = l
		}
	}
}

func (c *Cockpit) cmd(mode string) string { return command(mode, c.Session) }

func command(mode string, args ...string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "claude-status-go"
	}
	out := shellQuote(exe) + " " + mode
	for _, a := range args {
		out += " " + shellQuote(a)
	}
	return out
}

const sidebarWidth = "42"

// Toggle opens the cockpit in c.Session. From inside it, it returns the
// keyboard to the list, or closes the cockpit when the list already has it.
func (c *Cockpit) Toggle(currentWindow, currentPane string) error {
	ls := c.layouts()
	// Earlier versions could leave several cockpits: keep one.
	for len(ls) > 1 {
		drop := ls[0]
		if drop.Window == currentWindow || drop.Session == c.Session {
			drop = ls[len(ls)-1]
		}
		c.closeLayout(drop)
		ls = c.layouts()
	}
	if len(ls) == 1 {
		c.State = ls[0]
		if c.State.Dead {
			c.T.Respawn(c.State.Sidebar, c.cmd("sidebar"))
		}
		if c.State.Window == currentWindow {
			if currentPane != "" && currentPane != c.State.Sidebar {
				return c.T.SelectPane(c.State.Sidebar)
			}
			return c.Close()
		}
		if c.State.Session != c.Session {
			return c.MoveTo(c.Session)
		}
		return c.T.SelectWindow(c.State.Window)
	}

	slot, err := c.T.NewWindow(c.Session+":", WindowName, "", c.cmd("placeholder"))
	if err != nil {
		return err
	}
	sidebar, err := c.T.SplitLeft(slot, sidebarWidth, c.cmd("sidebar"))
	if err != nil {
		return err
	}
	c.T.Set("-p", slot, optRole, "slot")
	// A slot that dies must not take its borrowed tab with it.
	c.T.Set("-p", slot, "remain-on-exit", "on")
	c.T.Set("-p", sidebar, optRole, "sidebar")
	c.T.Set("-p", sidebar, "remain-on-exit", "on") // a crash keeps the cockpit findable
	c.T.Set("-p", sidebar, optSlot, slot)
	c.style(slot)
	c.load()
	return nil
}

// style gives the cockpit window herdr-like borders and a title bar over the
// shown pane.
func (c *Cockpit) style(target string) {
	for _, o := range [][2]string{
		{"pane-border-status", "top"},
		{"pane-border-lines", "single"},
		{"pane-border-style", "fg=#45475a"},
		{"pane-active-border-style", "fg=#cba6f7"},
		{"pane-border-format", "#{?" + optName + ",#[fg=#cba6f7#,bold] #{" + optName + "} #[default],}"},
	} {
		c.T.Set("-w", target, o[0], o[1])
	}
}

// MoveTo carries the cockpit into another session and switches the client there.
func (c *Cockpit) MoveTo(session string) error {
	c.Session = session
	c.load()
	if c.State.Window == "" {
		return fmt.Errorf("no cockpit")
	}
	// Left to itself tmux picks a client of the new session, not the user's.
	client := c.T.ActiveClient()
	if c.State.Session != session {
		if err := c.T.MoveWindow(c.State.Window, session); err != nil {
			return err
		}
		c.State.Session = session
	}
	return c.T.SwitchTo(client, c.State.Window)
}

// Show swaps a pane into the slot, returning any other borrowed pane first.
func (c *Cockpit) Show(pane, name string) error {
	c.load()
	l := c.State
	if l.Window == "" || pane == "" || pane == l.Borrowed || pane == l.Slot || pane == l.Sidebar {
		return nil
	}
	if err := c.Restore(); err != nil {
		return err
	}
	c.stopViewing()
	if err := c.T.Swap(pane, l.Slot); err != nil {
		return err
	}
	c.T.Set("-p", pane, optHome, l.Slot)
	c.T.Set("-p", pane, optName, name)
	c.load()
	return nil
}

// View shows a subagent transcript in the slot instead of a borrowed pane.
func (c *Cockpit) View(path, title string) error {
	c.load()
	if c.State.Window == "" || (c.State.Viewing == path && c.State.Borrowed == "") {
		return nil
	}
	if err := c.Restore(); err != nil {
		return err
	}
	slot := c.State.Slot
	if err := c.T.Respawn(slot, command("transcript", path, title)); err != nil {
		return err
	}
	c.T.Set("-p", slot, optView, path)
	c.T.Set("-p", slot, optName, title)
	c.load()
	return nil
}

// stopViewing puts the placeholder back in the slot before it's lent out.
func (c *Cockpit) stopViewing() {
	if c.State.Viewing == "" {
		return
	}
	c.T.Respawn(c.State.Slot, c.cmd("placeholder"))
	c.T.Unset(c.State.Slot, optView)
	c.T.Unset(c.State.Slot, optName)
	c.State.Viewing = ""
}

// Restore sends the borrowed pane back to its tab.
func (c *Cockpit) Restore() error {
	c.load()
	return c.restore(c.State)
}

func (c *Cockpit) restore(l Layout) error {
	if l.Borrowed == "" {
		return nil
	}
	c.T.Unset(l.Borrowed, optHome)
	if err := c.T.Swap(l.Borrowed, l.Slot); err != nil {
		// Its slot was closed, and with it the tab: give it a tab of its own.
		return c.T.BreakPane(l.Borrowed, l.Session, l.Name)
	}
	return nil
}

// Close returns any borrowed pane and removes the cockpit window.
func (c *Cockpit) Close() error {
	c.load()
	return c.closeLayout(c.State)
}

// closeLayout never kills an agent: a borrowed pane that can't go home (its
// slot is gone) gets a tab of its own first.
func (c *Cockpit) closeLayout(l Layout) error {
	if l.Window == "" {
		return nil
	}
	c.restore(l)
	for _, p := range c.panes() {
		if p.window == l.Window && p.id != l.Sidebar && p.id != l.Slot {
			name := p.title
			if name == "" {
				name = "rescued"
			}
			c.T.Unset(p.id, optHome)
			c.T.BreakPane(p.id, l.Session, name)
		}
	}
	return c.T.KillWindow(l.Window)
}
