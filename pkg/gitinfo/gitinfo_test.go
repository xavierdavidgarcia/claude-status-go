package gitinfo

import "testing"

func TestParseStatus(t *testing.T) {
	out := "# branch.oid abc\n" +
		"# branch.head feat/login\n" +
		"# branch.upstream origin/feat/login\n" +
		"# branch.ab +3 -1\n" +
		"1 M. N... 100644 100644 100644 h1 h2 README.md\n" +
		"1 .M N... 100644 100644 100644 h1 h2 src/state ts.go\n" +
		"1 MD N... 100644 100644 000000 h1 h2 both.go\n" +
		"2 R. N... 100644 100644 100644 h1 h2 R100 new.go\told.go\n" +
		"? NOTES.md\n"
	var i Info
	parseStatus(out, &i)

	if i.Branch != "feat/login" || i.Ahead != 3 || i.Behind != 1 {
		t.Fatalf("branch: %+v", i)
	}
	staged := paths(i.Staged)
	if want := "M README.md,M both.go,R new.go"; staged != want {
		t.Errorf("staged = %q, want %q", staged, want)
	}
	if got, want := paths(i.Unstaged), "M src/state ts.go,D both.go"; got != want {
		t.Errorf("unstaged = %q, want %q", got, want)
	}
	if got := paths(i.Untracked); got != "? NOTES.md" {
		t.Errorf("untracked = %q", got)
	}
}

func TestApplyNumstat(t *testing.T) {
	fs := []File{{Path: "a.go"}, {Path: "img.png"}}
	applyNumstat("4\t1\ta.go\n-\t-\timg.png\n", fs)
	if fs[0].Add != 4 || fs[0].Del != 1 || fs[1].Add != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestParseWorktrees(t *testing.T) {
	out := "worktree /src/app\nHEAD a\nbranch refs/heads/main\n\n" +
		"worktree /src/app/.claude/worktrees/fix\nHEAD b\nbranch refs/heads/worktree-fix\n\n" +
		"worktree /tmp/x\nHEAD c\ndetached\n"
	wts := parseWorktrees(out, "/src/app/.claude/worktrees/fix")
	if len(wts) != 3 || wts[0].Branch != "main" || !wts[1].Current || wts[2].Branch != "(detached)" {
		t.Fatalf("%+v", wts)
	}
}

func paths(fs []File) string {
	s := ""
	for i, f := range fs {
		if i > 0 {
			s += ","
		}
		s += string(f.Status) + " " + f.Path
	}
	return s
}

func TestParseLog(t *testing.T) {
	cs := parseLog("abc1234\t1700000000\tfeat: tabs\tand more\n")
	if len(cs) != 1 || cs[0].Hash != "abc1234" || cs[0].Subject != "feat: tabs\tand more" || cs[0].When.Unix() != 1700000000 {
		t.Fatalf("%+v", cs)
	}
}
