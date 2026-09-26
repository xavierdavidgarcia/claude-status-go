// Package cockpit implements the tmux side of the agent cockpit: a window with
// the agent list on the left and a slot on the right that an agent's real
// pane is swapped into.
package cockpit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xgarcia/claude-status-go/pkg/tmux"
)

const WindowName = "cockpit"

// State is persisted so a restarted sidebar can return a borrowed pane.
type State struct {
	Slot         string `json:"slot"`
	Sidebar      string `json:"sidebar"`
	Borrowed     string `json:"borrowed,omitempty"`
	BorrowedName string `json:"borrowed_name,omitempty"`
	Viewing      string `json:"viewing,omitempty"` // transcript shown in the slot
}

type Cockpit struct {
	T       *tmux.Client
	Session string
	Path    string
	State   State
}

// New returns the cockpit of a tmux session; each session has its own.
func New(t *tmux.Client, session string) *Cockpit {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\x00' {
			return '_'
		}
		return r
	}, session)
	c := &Cockpit{T: t, Session: session, Path: filepath.Join(dir, "claude-cockpit", "cockpit-"+name+".json")}
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

func (c *Cockpit) load() {
	if data, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(data, &c.State)
	}
}

func (c *Cockpit) save() error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(c.State)
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

const sidebarWidth = "42"

// cmd is the shell command running this binary in mode for this session.
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

// Toggle opens the session's cockpit window. From inside it, it returns the
// keyboard to the list, or closes the cockpit when the list already has it.
func (c *Cockpit) Toggle(currentWindow, currentPane string) error {
	session := c.Session
	if id := c.T.FindWindow(session, WindowName); id != "" {
		if id == currentWindow {
			c.load()
			if currentPane != "" && currentPane != c.State.Sidebar {
				return c.T.SelectPane(c.State.Sidebar)
			}
			return c.Close()
		}
		c.load()
		if _, ok := c.T.Panes()[c.State.Sidebar]; !ok {
			sidebar, err := c.T.SplitLeft(c.State.Slot, sidebarWidth, c.cmd("sidebar"))
			if err != nil {
				return err
			}
			c.State.Sidebar = sidebar
			c.save()
		}
		return c.T.SelectWindow(id)
	}
	c.Restore() // a pane left borrowed by a cockpit that no longer exists
	slot, err := c.T.NewWindow(session+":", WindowName, "", c.cmd("placeholder"))
	if err != nil {
		return err
	}
	sidebar, err := c.T.SplitLeft(slot, sidebarWidth, c.cmd("sidebar"))
	if err != nil {
		return err
	}
	c.style(slot)
	c.State = State{Slot: slot, Sidebar: sidebar}
	return c.save()
}

// style gives the cockpit window herdr-like borders and a title bar over the
// shown pane, set by Show through the pane's @cockpit_title.
func (c *Cockpit) style(target string) {
	for _, o := range [][2]string{
		{"pane-border-status", "top"},
		{"pane-border-lines", "single"},
		{"pane-border-style", "fg=#45475a"},
		{"pane-active-border-style", "fg=#cba6f7"},
		{"pane-border-format", "#{?@cockpit_title,#[fg=#cba6f7#,bold] #{@cockpit_title} #[default],}"},
	} {
		c.T.Set("-w", target, o[0], o[1])
	}
}

// MoveTo carries the cockpit into another session and switches the client
// there, replacing that session's own cockpit if it had one.
func (c *Cockpit) MoveTo(session string) error {
	c.load()
	if session == c.Session {
		return nil
	}
	win := c.T.Panes()[c.State.Sidebar].Window
	if win == "" {
		return fmt.Errorf("cockpit window not found")
	}
	if c.T.FindWindow(session, WindowName) != "" {
		New(c.T, session).Close()
	}
	if err := c.T.MoveWindow(win, session); err != nil {
		return err
	}
	old := c.Path
	*c = *New(c.T, session)
	c.load()
	if data, err := os.ReadFile(old); err == nil {
		os.WriteFile(c.Path, data, 0o600)
		os.Remove(old)
		c.load()
	}
	return c.T.SwitchTo(win)
}

// Show swaps an agent's pane into the cockpit slot, returning any other
// borrowed pane first.
func (c *Cockpit) Show(pane, name string) error {
	c.load()
	if pane == "" || pane == c.State.Borrowed || pane == c.State.Slot || pane == c.State.Sidebar {
		return nil
	}
	if err := c.Restore(); err != nil {
		return err
	}
	c.stopViewing()
	if err := c.T.Swap(pane, c.State.Slot); err != nil {
		return err
	}
	c.T.Set("-p", pane, "@cockpit_title", name)
	c.State.Borrowed, c.State.BorrowedName = pane, name
	return c.save()
}

// View shows a subagent transcript in the slot instead of a borrowed pane.
func (c *Cockpit) View(path, title string) error {
	c.load()
	if c.State.Viewing == path && c.State.Borrowed == "" {
		return nil
	}
	if err := c.Restore(); err != nil {
		return err
	}
	if err := c.T.Respawn(c.State.Slot, command("transcript", path, title)); err != nil {
		return err
	}
	c.T.Set("-p", c.State.Slot, "@cockpit_title", title)
	c.State.Viewing = path
	return c.save()
}

// stopViewing puts the placeholder back in the slot before it's lent out.
func (c *Cockpit) stopViewing() {
	if c.State.Viewing == "" {
		return
	}
	c.T.Respawn(c.State.Slot, c.cmd("placeholder"))
	c.T.Set("-p", c.State.Slot, "@cockpit_title", "")
	c.State.Viewing = ""
	c.save()
}

// Renamed follows a rename of the cockpit's session.
func (c *Cockpit) Renamed(name string) {
	old := c.Path
	c.load()
	st := c.State
	*c = *New(c.T, name)
	c.State = st
	c.save()
	os.Remove(old)
}

// Close returns any borrowed pane and removes the cockpit window.
func (c *Cockpit) Close() error {
	if err := c.Restore(); err != nil {
		return err
	}
	return c.T.KillWindow(c.State.Slot)
}

// Restore sends the borrowed pane back to its own window.
func (c *Cockpit) Restore() error {
	c.load()
	if c.State.Borrowed == "" {
		return nil
	}
	panes := c.T.Panes()
	_, okB := panes[c.State.Borrowed]
	_, okS := panes[c.State.Slot]
	if okB && okS {
		if err := c.T.Swap(c.State.Borrowed, c.State.Slot); err != nil {
			return err
		}
	}
	c.State.Borrowed, c.State.BorrowedName = "", ""
	return c.save()
}
