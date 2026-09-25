// Package agents discovers running Claude Code sessions from the per-process
// registry files Claude writes to <config>/sessions/<pid>.json. The schema is
// internal to Claude Code, so parsing is lenient.
package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type State int

// Order is the display order: what needs you first.
const (
	NeedsYou State = iota
	Working
	Shell
	Idle
	Unknown
)

func (s State) String() string {
	return [...]string{"needs you", "working", "shell", "idle", "unknown"}[s]
}

type Agent struct {
	PID        int
	SessionID  string
	Name       string
	Cwd        string
	ConfigDir  string
	State      State
	WaitingFor string
	Since      time.Time
	PaneID     string // "%99"; empty when not running in tmux
}

func (a Agent) Project() string { return filepath.Base(a.Cwd) }

type registry struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	ProcStart       string `json:"procStart"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	WaitingFor      string `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	Tmux            string `json:"tmux"` // "session:@window.%pane"
}

// FS is the slice of the OS the scanner needs; faked in tests.
type FS interface {
	ReadFile(path string) ([]byte, error)
	Glob(pattern string) ([]string, error)
}

type osFS struct{}

func (osFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (osFS) Glob(p string) ([]string, error)   { return filepath.Glob(p) }

type Scanner struct {
	FS      FS
	Proc    string // "/proc"
	Sockets string // "/run/user/<uid>/cc-socks"
	Extra   []string
}

func NewScanner() *Scanner {
	s := &Scanner{FS: osFS{}, Proc: "/proc", Sockets: fmt.Sprintf("/run/user/%d/cc-socks", os.Getuid())}
	home, _ := os.UserHomeDir()
	s.Extra = append(s.Extra, filepath.Join(home, ".claude"))
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		s.Extra = append(s.Extra, d)
	}
	return s
}

// ConfigDirs finds every config dir with a live Claude: each messaging socket
// is named after a pid, whose environment holds its CLAUDE_CONFIG_DIR.
func (s *Scanner) ConfigDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, d := range s.Extra {
		add(d)
	}
	socks, _ := s.FS.Glob(filepath.Join(s.Sockets, "*.sock"))
	for _, sock := range socks {
		pid := strings.TrimSuffix(filepath.Base(sock), ".sock")
		env, err := s.FS.ReadFile(filepath.Join(s.Proc, pid, "environ"))
		if err != nil {
			continue
		}
		if dir, ok := envValue(env, "CLAUDE_CONFIG_DIR"); ok {
			add(dir)
		} else if home, ok := envValue(env, "HOME"); ok {
			add(filepath.Join(home, ".claude"))
		}
	}
	return dirs
}

func envValue(env []byte, key string) (string, bool) {
	for _, kv := range strings.Split(string(env), "\x00") {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// Scan returns live agents, most urgent first.
func (s *Scanner) Scan() []Agent {
	var out []Agent
	seen := map[int]bool{}
	for _, dir := range s.ConfigDirs() {
		files, _ := s.FS.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, f := range files {
			data, err := s.FS.ReadFile(f)
			if err != nil {
				continue
			}
			var r registry
			if json.Unmarshal(data, &r) != nil || r.PID == 0 || seen[r.PID] {
				continue
			}
			if !s.alive(r.PID, r.ProcStart) {
				continue
			}
			seen[r.PID] = true
			out = append(out, fromRegistry(r, dir))
		}
	}
	Sort(out)
	return out
}

// alive rejects dead pids and pids reused by another process: procStart is
// field 22 (starttime) of /proc/<pid>/stat.
func (s *Scanner) alive(pid int, procStart string) bool {
	stat, err := s.FS.ReadFile(filepath.Join(s.Proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	if procStart == "" {
		return true
	}
	// comm (field 2) may contain spaces; fields after ")" start at field 3.
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return false
	}
	fields := strings.Fields(string(stat[i+1:]))
	return len(fields) > 19 && fields[19] == procStart
}

func fromRegistry(r registry, dir string) Agent {
	a := Agent{
		PID:        r.PID,
		SessionID:  r.SessionID,
		Name:       r.Name,
		Cwd:        r.Cwd,
		ConfigDir:  dir,
		WaitingFor: r.WaitingFor,
	}
	if a.Name == "" {
		a.Name = filepath.Base(r.Cwd)
	}
	if r.StatusUpdatedAt > 0 {
		a.Since = time.UnixMilli(r.StatusUpdatedAt)
	}
	if i := strings.LastIndexByte(r.Tmux, '.'); i >= 0 && strings.HasPrefix(r.Tmux[i+1:], "%") {
		a.PaneID = r.Tmux[i+1:]
	}
	switch r.Status {
	case "waiting":
		a.State = NeedsYou
	case "busy":
		a.State = Working
	case "shell":
		a.State = Shell
	case "idle":
		a.State = Idle
	default:
		a.State = Unknown
	}
	return a
}

// Sort orders by state, then longest in that state first.
func Sort(as []Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		if as[i].State != as[j].State {
			return as[i].State < as[j].State
		}
		return as[i].Since.Before(as[j].Since)
	})
}

// Counts returns how many agents are in each state.
func Counts(as []Agent) map[State]int {
	c := map[State]int{}
	for _, a := range as {
		c[a.State]++
	}
	return c
}
