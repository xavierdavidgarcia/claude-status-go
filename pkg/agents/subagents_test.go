package agents

import (
	"os"
	"path/filepath"
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
	write("old", "long done", done, time.Hour)         // finished too long ago
	write("dead", "interrupted", busy, 10*time.Minute) // unfinished but silent

	s := &Scanner{FS: diskFS{}}
	got := s.Subagents(Agent{SessionID: "sess1", ConfigDir: cfg}, now)
	if len(got) != 2 || got[0].Desc != "code review" || !got[0].Running || got[1].Desc != "explore repo" || got[1].Running {
		t.Fatalf("%+v", got)
	}
}
