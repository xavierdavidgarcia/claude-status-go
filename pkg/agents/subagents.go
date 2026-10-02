package agents

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type SubState int

const (
	SubRunning SubState = iota
	SubDone
	SubStopped // unfinished and silent: interrupted or killed with its session
)

// Sub is a subagent (Agent/Task tool) launched by a Claude session. Claude
// writes each one to <config>/projects/<project>/<session>/subagents/.
type Sub struct {
	ID    string
	Desc  string
	Type  string
	State SubState
	When  time.Time // last transcript write
	Path  string    // transcript (.jsonl)
}

const subSilent = 5 * time.Minute // unfinished and quiet this long: stopped

type subCache struct {
	mu    sync.Mutex
	byKey map[string]bool // "path|size|mtime" → finished
}

var finishedCache = subCache{byKey: map[string]bool{}}

// Subagents lists all of a session's subagents: running first, then newest.
func (s *Scanner) Subagents(a Agent, now time.Time) []Sub {
	if a.SessionID == "" || a.ConfigDir == "" {
		return nil
	}
	metas, _ := s.FS.Glob(filepath.Join(a.ConfigDir, "projects", "*", a.SessionID, "subagents", "agent-*.meta.json"))
	var out []Sub
	for _, meta := range metas {
		base := strings.TrimSuffix(meta, ".meta.json")
		path := base + ".jsonl"
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		var m struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
		}
		if data, err := s.FS.ReadFile(meta); err == nil {
			_ = json.Unmarshal(data, &m)
		}
		state := SubDone
		if !cachedFinished(path, st) {
			state = SubRunning
			if now.Sub(st.ModTime()) > subSilent {
				state = SubStopped
			}
		}
		out = append(out, Sub{
			ID:    strings.TrimPrefix(filepath.Base(base), "agent-"),
			Desc:  m.Description,
			Type:  m.AgentType,
			State: state,
			When:  st.ModTime(),
			Path:  path,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == SubRunning) != (out[j].State == SubRunning) {
			return out[i].State == SubRunning
		}
		return out[i].When.After(out[j].When)
	})
	return out
}

// cachedFinished re-reads a transcript's tail only when it has changed.
func cachedFinished(path string, st os.FileInfo) bool {
	k := path + "|" + st.ModTime().String()
	finishedCache.mu.Lock()
	done, ok := finishedCache.byKey[k]
	finishedCache.mu.Unlock()
	if ok {
		return done
	}
	done = finished(path)
	finishedCache.mu.Lock()
	finishedCache.byKey[k] = done
	finishedCache.mu.Unlock()
	return done
}

// finished reports whether a transcript ends with the subagent's final answer.
func finished(path string) bool {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			StopReason string `json:"stop_reason"`
		} `json:"message"`
	}
	return json.Unmarshal(LastLine(path), &e) == nil && e.Type == "assistant" && e.Message.StopReason == "end_turn"
}

// LastLine reads only the tail of a possibly large transcript.
func LastLine(path string) []byte {
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
