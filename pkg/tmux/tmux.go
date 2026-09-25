// Package tmux wraps the tmux commands the cockpit needs. Panes are always
// addressed by id (%N), which survives swaps between windows.
package tmux

import (
	"os/exec"
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
}

func (p Pane) Location() string { return p.Session + ":" + p.Index }

// Panes maps pane id to its current location.
func (c *Client) Panes() map[string]Pane {
	out, err := c.R.Run("list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{window_id}\t#{window_index}\t#{window_name}")
	panes := map[string]Pane{}
	if err != nil {
		return panes
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 5 {
			panes[f[0]] = Pane{ID: f[0], Session: f[1], Window: f[2], Index: f[3], WindowName: f[4]}
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
