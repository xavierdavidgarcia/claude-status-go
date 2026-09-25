package agents

import (
	"os"
	"path"
	"testing"
	"time"
)

type fakeFS map[string]string

func (f fakeFS) ReadFile(p string) ([]byte, error) {
	if s, ok := f[p]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (f fakeFS) Glob(pattern string) ([]string, error) {
	var out []string
	for p := range f {
		if ok, _ := path.Match(pattern, p); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// stat builds a /proc/<pid>/stat line whose field 22 is start; comm has a space.
func stat(start string) string {
	return "1 (claude x) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 " + start + " 20"
}

func TestScan(t *testing.T) {
	fs := fakeFS{
		"/run/cc/10.sock":  "",
		"/run/cc/20.sock":  "",
		"/proc/10/environ": "HOME=/h\x00CLAUDE_CONFIG_DIR=/cfgA",
		"/proc/20/environ": "HOME=/h",
		"/proc/10/stat":    stat("111"),
		"/proc/20/stat":    stat("222"),
		"/proc/30/stat":    stat("999"), // pid reused by another process
		"/cfgA/sessions/10.json": `{"pid":10,"name":"infra","status":"waiting","waitingFor":"permission prompt",
			"procStart":"111","statusUpdatedAt":1000,"tmux":"0:@8.%12","extra":{"ignored":true}}`,
		"/h/.claude/sessions/20.json":  `{"pid":20,"cwd":"/src/papyrus","status":"busy","procStart":"222"}`,
		"/h/.claude/sessions/30.json":  `{"pid":30,"name":"gone","status":"idle","procStart":"333"}`,
		"/h/.claude/sessions/40.json":  `{"pid":40,"name":"dead","status":"idle"}`,
		"/h/.claude/sessions/bad.json": `not json`,
	}
	s := &Scanner{FS: fs, Proc: "/proc", Sockets: "/run/cc"}
	got := s.Scan()

	if len(got) != 2 {
		t.Fatalf("want 2 live agents, got %d: %+v", len(got), got)
	}
	a, b := got[0], got[1]
	if a.Name != "infra" || a.State != NeedsYou || a.WaitingFor != "permission prompt" || a.PaneID != "%12" || a.TmuxSess != "0" || a.ConfigDir != "/cfgA" {
		t.Errorf("first agent wrong: %+v", a)
	}
	if b.Name != "papyrus" || b.State != Working || b.PaneID != "" {
		t.Errorf("second agent should fall back to cwd name and have no pane: %+v", b)
	}
}

func TestSortByStateThenAge(t *testing.T) {
	as := []Agent{
		{Name: "idle", State: Idle},
		{Name: "new-busy", State: Working, Since: at(20)},
		{Name: "old-busy", State: Working, Since: at(10)},
		{Name: "wait", State: NeedsYou},
	}
	Sort(as)
	want := []string{"wait", "old-busy", "new-busy", "idle"}
	for i, n := range want {
		if as[i].Name != n {
			t.Fatalf("pos %d: want %s, got %s", i, n, as[i].Name)
		}
	}
}

func at(sec int64) time.Time { return time.Unix(sec, 0) }
