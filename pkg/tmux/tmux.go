// Package tmux wraps the tmux commands the cockpit needs. Panes are always
// addressed by id (%N), which survives swaps between windows.
package tmux

import (
	"os/exec"
	"strconv"
	"strings"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type Exec struct{}

func (Exec) Run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

type Client struct{ R Runner }

func New() *Client { return &Client{R: Exec{}} }

type Pane struct {
	ID         string
	Session    string
	Window     string // "@8"
	WindowName string
	Index      string // window index
	Active     bool   // active pane of its window
	Path       string // pane_current_path
	WinActive  bool   // its window is the session's current one
	PID        int    // pane_pid: the pane's first process
}

func (p Pane) Location() string { return p.Session + ":" + p.Index }

// Panes maps pane id to its current location.
func (c *Client) Panes() map[string]Pane {
	out, err := c.R.Run("list-panes", "-a", "-F",
		"#{pane_id}\t#{session_name}\t#{window_id}\t#{window_index}\t#{window_name}\t#{pane_active}\t#{pane_current_path}\t#{window_active}\t#{pane_pid}")
	panes := map[string]Pane{}
	if err != nil {
		return panes
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 9 {
			pid, _ := strconv.Atoi(f[8])
			panes[f[0]] = Pane{ID: f[0], Session: f[1], Window: f[2], Index: f[3], WindowName: f[4],
				Active: f[5] == "1", Path: f[6], WinActive: f[7] == "1", PID: pid}
		}
	}
	return panes
}

// Jump focuses a pane from any session.
func (c *Client) Jump(pane string) error {
	if _, err := c.R.Run("switch-client", "-t", pane); err != nil {
		return err
	}
	if _, err := c.R.Run("select-window", "-t", pane); err != nil {
		return err
	}
	_, err := c.R.Run("select-pane", "-t", pane)
	return err
}

// Swap exchanges two panes' positions, across windows and sessions.
func (c *Client) Swap(a, b string) error {
	_, err := c.R.Run("swap-pane", "-d", "-s", a, "-t", b)
	return err
}

// Send types text literally, then submits it as a separate key so the text
// can't be read as key names.
func (c *Client) Send(pane, text string) error {
	if _, err := c.R.Run("send-keys", "-t", pane, "-l", text); err != nil {
		return err
	}
	_, err := c.R.Run("send-keys", "-t", pane, "Enter")
	return err
}

// FindWindow returns the id of the first window with this name in session.
func (c *Client) FindWindow(session, name string) string {
	out, err := c.R.Run("list-windows", "-t", session, "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if id, n, ok := strings.Cut(line, "\t"); ok && n == name {
			return id
		}
	}
	return ""
}

// Display evaluates a format string, e.g. "#{session_name}".
func (c *Client) Display(target, format string) (string, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	return c.R.Run(append(args, format)...)
}

// NewWindow runs cmd in a new window and returns the new pane id.
func (c *Client) NewWindow(target, name, dir, cmd string) (string, error) {
	args := []string{"new-window", "-P", "-F", "#{pane_id}", "-n", name}
	if target != "" {
		args = append(args, "-t", target)
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	return c.R.Run(append(args, cmd)...)
}

// SplitLeft opens a pane of the given width to the left of target.
func (c *Client) SplitLeft(target, width, cmd string) (string, error) {
	return c.R.Run("split-window", "-h", "-b", "-l", width, "-t", target, "-P", "-F", "#{pane_id}", cmd)
}

func (c *Client) SelectWindow(target string) error {
	_, err := c.R.Run("select-window", "-t", target)
	return err
}

func (c *Client) SelectPane(target string) error {
	_, err := c.R.Run("select-pane", "-t", target)
	return err
}

func (c *Client) KillWindow(target string) error {
	_, err := c.R.Run("kill-window", "-t", target)
	return err
}

// Popup runs cmd in dir, in a popup over the client viewing session.
func (c *Client) Popup(session, dir, cmd string) error {
	args := []string{"display-popup", "-E", "-w", "90%", "-h", "85%", "-d", dir}
	if out, err := c.R.Run("list-clients", "-F", "#{client_name}\t#{client_session}"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if name, s, ok := strings.Cut(l, "\t"); ok && s == session {
				args = append(args, "-c", name)
				break
			}
		}
	}
	_, err := c.R.Run(append(args, cmd)...)
	return err
}

// Set sets an option; scope is "-w" (window) or "-p" (pane).
func (c *Client) Set(scope, target, name, value string) error {
	_, err := c.R.Run("set-option", scope, "-t", target, name, value)
	return err
}

// MoveWindow moves a window to the end of another session.
func (c *Client) MoveWindow(window, session string) error {
	_, err := c.R.Run("move-window", "-d", "-s", window, "-t", session+":")
	return err
}

// SwitchTo shows target (a window or pane) on the attached client.
func (c *Client) SwitchTo(target string) error {
	if _, err := c.R.Run("switch-client", "-t", target); err != nil {
		return err
	}
	_, err := c.R.Run("select-window", "-t", target)
	return err
}

// Respawn replaces the program running in a pane.
func (c *Client) Respawn(pane, cmd string) error {
	_, err := c.R.Run("respawn-pane", "-k", "-t", pane, cmd)
	return err
}

func (c *Client) RenameSession(old, name string) error {
	_, err := c.R.Run("rename-session", "-t", old, name)
	return err
}

// Unset removes a pane option.
func (c *Client) Unset(pane, name string) error {
	_, err := c.R.Run("set-option", "-p", "-u", "-t", pane, name)
	return err
}

// BreakPane moves a pane into a new window of session, in the background.
func (c *Client) BreakPane(pane, session, name string) error {
	_, err := c.R.Run("break-pane", "-d", "-s", pane, "-t", session+":", "-n", name)
	return err
}
