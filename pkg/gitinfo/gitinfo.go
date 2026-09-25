// Package gitinfo collects the git state shown in the cockpit's bottom panel.
package gitinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type File struct {
	Status   byte // 'A', 'M', 'D', 'R', '?'
	Path     string
	Add, Del int
}

type Commit struct {
	Hash, Subject string
	When          time.Time
}

type Worktree struct {
	Path, Branch string
	Current      bool
}

type Info struct {
	Dir       string
	Repo      bool
	Branch    string
	Ahead     int
	Behind    int
	Staged    []File
	Unstaged  []File
	Untracked []File
	Log       []Commit
	Worktrees []Worktree
	At        time.Time
}

func (i Info) Totals() (add, del, files int) {
	for _, fs := range [][]File{i.Staged, i.Unstaged} {
		for _, f := range fs {
			add += f.Add
			del += f.Del
		}
	}
	return add, del, len(i.Staged) + len(i.Unstaged) + len(i.Untracked)
}

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// --no-optional-locks: never contend with the user's own git commands.
	out, err := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", dir}, args...)...).Output()
	return string(out), err
}

func Load(dir string) Info {
	info := Info{Dir: dir, At: time.Now()}
	st, err := git(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return info
	}
	info.Repo = true
	parseStatus(st, &info)
	if out, err := git(dir, "diff", "--cached", "--numstat"); err == nil {
		applyNumstat(out, info.Staged)
	}
	if out, err := git(dir, "diff", "--numstat"); err == nil {
		applyNumstat(out, info.Unstaged)
	}
	if out, err := git(dir, "log", "-n", "15", "--format=%h%x09%ct%x09%s"); err == nil {
		info.Log = parseLog(out)
	}
	if out, err := git(dir, "worktree", "list", "--porcelain"); err == nil {
		top, _ := git(dir, "rev-parse", "--show-toplevel")
		info.Worktrees = parseWorktrees(out, strings.TrimSpace(top))
	}
	return info
}

func parseStatus(out string, info *Info) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			info.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					info.Ahead = n
				} else {
					info.Behind = n
				}
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			// "1 XY sub mH mI mW hH hI path"; renames ("2") add a score and "path\torig".
			n := 8
			if line[0] == '2' {
				n = 9
			}
			f := strings.SplitN(line, " ", n+1)
			if len(f) <= n || len(f[1]) != 2 {
				continue
			}
			path, _, _ := strings.Cut(f[n], "\t")
			x, y := f[1][0], f[1][1]
			if x != '.' {
				info.Staged = append(info.Staged, File{Status: x, Path: path})
			}
			if y != '.' {
				info.Unstaged = append(info.Unstaged, File{Status: y, Path: path})
			}
		case strings.HasPrefix(line, "? "):
			info.Untracked = append(info.Untracked, File{Status: '?', Path: line[2:]})
		}
	}
}

func applyNumstat(out string, files []File) {
	counts := map[string][2]int{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		a, _ := strconv.Atoi(f[0]) // "-" for binary files stays 0
		d, _ := strconv.Atoi(f[1])
		counts[f[2]] = [2]int{a, d}
	}
	for i := range files {
		c := counts[files[i].Path]
		files[i].Add, files[i].Del = c[0], c[1]
	}
}

func parseWorktrees(out, current string) []Worktree {
	var wts []Worktree
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var w Worktree
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "worktree "); ok {
				w.Path = v
			} else if v, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				w.Branch = v
			} else if line == "detached" {
				w.Branch = "(detached)"
			}
		}
		if w.Path != "" {
			w.Current = w.Path == current
			wts = append(wts, w)
		}
	}
	return wts
}

func parseLog(out string) []Commit {
	var cs []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(f[1], 10, 64)
		cs = append(cs, Commit{Hash: f[0], Subject: f[2], When: time.Unix(ts, 0)})
	}
	return cs
}

// Branch is the current branch of dir, or "" outside a repo.
func Branch(dir string) string {
	out, err := git(dir, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
