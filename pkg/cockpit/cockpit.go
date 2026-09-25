// Package cockpit implements the tmux side of the agent cockpit: a window with
// the agent list on the left and a slot on the right that an agent's real
// pane is swapped into.
package cockpit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	T     *tmux.Client
	Path  string
	State State
}

func New(t *tmux.Client) *Cockpit {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	c := &Cockpit{T: t, Path: filepath.Join(dir, "claude-cockpit", "cockpit.json")}
	c.load()
	return c
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

func self() string {
	exe, err := os.Executable()
	if err != nil {
		return "claude-status-go"
	}
	return exe
}

// Open focuses the cockpit window in session (default: the current one),
// creating it if needed.
func (c *Cockpit) Open(session string) error {
	if session == "" {
		var err error
		if session, err = c.T.Display("", "#{session_name}"); err != nil {
			return fmt.Errorf("not inside tmux: %w", err)
		}
	}
	if id := c.T.FindWindow(session, WindowName); id != "" {
		c.load()
		if _, ok := c.T.Panes()[c.State.Sidebar]; !ok {
			sidebar, err := c.T.SplitLeft(c.State.Slot, "36", self()+" sidebar")
			if err != nil {
				return err
			}
			c.State.Sidebar = sidebar
			c.save()
		}
		return c.T.SelectWindow(id)
	}
	c.Restore() // a pane left borrowed by a cockpit that no longer exists
	slot, err := c.T.NewWindow(session+":", WindowName, "", self()+" placeholder")
	if err != nil {
		return err
	}
	sidebar, err := c.T.SplitLeft(slot, "36", self()+" sidebar")
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
