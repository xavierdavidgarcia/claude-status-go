package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type diskFS struct{}

func (diskFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (diskFS) Glob(p string) ([]string, error)   { return filepath.Glob(p) }

func TestSubagents(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "projects", "-src-app", "sess1", "subagents")
	os.MkdirAll(dir, 0o755)
	now := time.Now()
	write := func(id, desc, last string, age time.Duration) {
		base := filepath.Join(dir, "agent-"+id)
		os.WriteFile(base+".meta.json", []byte(`{"agentType":"general-purpose","description":"`+desc+`"}`), 0o644)
		os.WriteFile(base+".jsonl", []byte(`{"type":"user"}`+"\n"+last+"\n"), 0o644)
		os.Chtimes(base+".jsonl", now.Add(-age), now.Add(-age))
	}
	const done = `{"type":"assistant","message":{"stop_reason":"end_turn"}}`
	const busy = `{"type":"assistant","message":{"stop_reason":"tool_use"}}`
	write("run", "code review", busy, 10*time.Second)
	write("fin", "explore repo", done, 2*time.Minute)
	write("old", "long done", done, time.Hour)
	write("dead", "interrupted", busy, 10*time.Minute) // unfinished but silent

	s := &Scanner{FS: diskFS{}}
	var summary []string
	for _, g := range s.Subagents(Agent{SessionID: "sess1", ConfigDir: cfg}, now) {
		summary = append(summary, fmt.Sprintf("%s:%d", g.Desc, g.State))
	}
	// Running first, then newest; silent unfinished ones are stopped.
	want := "code review:0 explore repo:1 interrupted:2 long done:1"
	if got := strings.Join(summary, " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
