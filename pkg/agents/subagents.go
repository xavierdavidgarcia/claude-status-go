package agents

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sub is a subagent (Agent/Task tool) launched by a Claude session. Claude
// writes each one to <config>/projects/<project>/<session>/subagents/.
type Sub struct {
	ID      string
	Desc    string
	Type    string
	Running bool
	When    time.Time // last transcript write
}

const (
	subRecent = 15 * time.Minute // finished subagents stay listed this long
	subStale  = 5 * time.Minute  // unfinished but silent this long: gone
	subMax    = 4
)

// Subagents lists a session's running and recently finished subagents,
// newest first.
func (s *Scanner) Subagents(a Agent, now time.Time) []Sub {
	if a.SessionID == "" || a.ConfigDir == "" {
		return nil
	}
	metas, _ := s.FS.Glob(filepath.Join(a.ConfigDir, "projects", "*", a.SessionID, "subagents", "agent-*.meta.json"))
	var out []Sub
	for _, meta := range metas {
		base := strings.TrimSuffix(meta, ".meta.json")
		st, err := os.Stat(base + ".jsonl")
		if err != nil || now.Sub(st.ModTime()) > subRecent {
			continue
		}
		var m struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
		}
		if data, err := s.FS.ReadFile(meta); err == nil {
			_ = json.Unmarshal(data, &m)
		}
		done := finished(base + ".jsonl")
		if !done && now.Sub(st.ModTime()) > subStale {
			continue
		}
		out = append(out, Sub{
			ID:      strings.TrimPrefix(filepath.Base(base), "agent-"),
			Desc:    m.Description,
			Type:    m.AgentType,
			Running: !done,
			When:    st.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return out[i].When.After(out[j].When)
	})
	if len(out) > subMax {
		out = out[:subMax]
	}
	return out
}

// finished reports whether a transcript ends with the subagent's final answer.
func finished(path string) bool {
	last := lastLine(path)
	var e struct {
		Type    string `json:"type"`
		Message struct {
			StopReason string `json:"stop_reason"`
		} `json:"message"`
	}
	return json.Unmarshal(last, &e) == nil && e.Type == "assistant" && e.Message.StopReason == "end_turn"
}

// lastLine reads only the tail of a possibly large transcript.
func lastLine(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	const tail = 256 << 10
	off := max(st.Size()-tail, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	buf = bytes.TrimRight(buf, "\n")
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	}
	return buf
}
