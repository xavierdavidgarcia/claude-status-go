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
func (c *Cockpit) cmd(mode string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "claude-status-go"
	}
	return shellQuote(exe) + " " + mode + " " + shellQuote(c.Session)
}

// Toggle opens the session's cockpit window, or closes it when the client is
// already looking at it (currentWindow is the client's window id).
func (c *Cockpit) Toggle(currentWindow string) error {
	session := c.Session
	if id := c.T.FindWindow(session, WindowName); id != "" {
		if id == currentWindow {
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
	c.State = State{Slot: slot, Sidebar: sidebar}
	return c.save()
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
	if err := c.T.Swap(pane, c.State.Slot); err != nil {
		return err
	}
	c.State.Borrowed, c.State.BorrowedName = pane, name
	return c.save()
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
