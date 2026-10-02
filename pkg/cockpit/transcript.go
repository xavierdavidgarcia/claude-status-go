package cockpit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Transcript follows a subagent's transcript in the cockpit slot.
func Transcript(path, title string) error {
	_, err := tea.NewProgram(&viewer{path: path, title: title}, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

type event struct {
	kind string // prompt, text, tool, result
	text string
}

type content struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
}

// parseTranscript turns transcript lines into what a person would want to see:
// the prompt, what the subagent says, the tools it runs and their results.
func parseTranscript(data []byte) (evs []event, done bool) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Content    json.RawMessage `json:"content"`
				StopReason string          `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		var parts []content
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			parts = []content{{Type: "text", Text: s}}
		} else {
			_ = json.Unmarshal(e.Message.Content, &parts)
		}
		for _, p := range parts {
			switch {
			case e.Type == "user" && p.Type == "text" && len(evs) == 0:
				evs = append(evs, event{"prompt", p.Text})
			case e.Type == "user" && p.Type == "tool_result":
				evs = append(evs, event{"result", resultText(p.Content)})
			case e.Type == "assistant" && p.Type == "text" && strings.TrimSpace(p.Text) != "":
				evs = append(evs, event{"text", p.Text})
			case e.Type == "assistant" && p.Type == "tool_use":
				evs = append(evs, event{"tool", p.Name + "(" + toolArg(p.Input) + ")"})
			}
		}
		done = e.Type == "assistant" && e.Message.StopReason == "end_turn"
	}
	return evs, done
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []content
	_ = json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// toolArg picks the one input that says what a tool call does.
func toolArg(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := in[k].(string); ok {
			return strings.ReplaceAll(v, "\n", " ")
		}
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

type viewer struct {
	path, title   string
	events        []event
	done          bool
	size          int64
	width, height int
	scroll        int // lines up from the bottom
}

type reloadMsg struct{}

func reload() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reloadMsg{} })
}

func (v *viewer) load() {
	st, err := os.Stat(v.path)
	if err != nil || st.Size() == v.size {
		return
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		return
	}
	v.size = st.Size()
	v.events, v.done = parseTranscript(data)
}

func (v *viewer) Init() tea.Cmd { v.load(); return reload() }

func (v *viewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
	case reloadMsg:
		v.load()
		return v, reload()
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			v.scroll += 3
		case tea.MouseButtonWheelDown:
			v.scroll = max(v.scroll-3, 0)
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "k", "up":
			v.scroll++
		case "j", "down":
			v.scroll = max(v.scroll-1, 0)
		case "pgup", "b":
			v.scroll += v.height / 2
		case "pgdown", "f", " ":
			v.scroll = max(v.scroll-v.height/2, 0)
		case "G", "end":
			v.scroll = 0
		}
	}
	return v, nil
}

func wrap(s string, w int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		r := []rune(l)
		for len(r) > w {
			out = append(out, string(r[:w]))
			r = r[w:]
		}
		out = append(out, string(r))
	}
	return out
}

func (v *viewer) View() string {
	w := max(v.width-4, 10)
	var lines []string
	add := func(st lipgloss.Style, prefix, text string, maxLines int) {
		ls := wrap(text, w-len([]rune(prefix)))
		if maxLines > 0 && len(ls) > maxLines {
			ls = append(ls[:maxLines], fmt.Sprintf("… %d more lines", len(ls)-maxLines))
		}
		for i, l := range ls {
			p := prefix
			if i > 0 {
				p = strings.Repeat(" ", len([]rune(prefix)))
			}
			lines = append(lines, "  "+sDim.Render(p)+st.Render(l))
		}
	}
	for _, e := range v.events {
		switch e.kind {
		case "prompt":
			add(sSub, "› ", e.text, 8)
			lines = append(lines, "")
		case "text":
			add(sText, "⏺ ", e.text, 0)
		case "tool":
			add(sPeach, "⏺ ", e.text, 2)
		case "result":
			add(sDim, "  ⎿ ", e.text, 3)
		}
	}

	status := seg{sYellow, "running"}
	if v.done {
		status = seg{sGreen, "done"}
	}
	head := line(max(v.width, 10), false, []seg{{sAccent.Bold(true), " " + trunc(v.title, v.width-12)}}, []seg{status, {sDim, " "}})
	room := max(v.height-2, 1)
	end := max(len(lines)-v.scroll, min(room, len(lines)))
	v.scroll = len(lines) - end
	start := max(end-room, 0)
	body := lines[start:end]
	for len(body) < room {
		body = append(body, "")
	}
	hint := "↑↓ scroll · G follow"
	if v.scroll > 0 {
		hint = fmt.Sprintf("%d lines below · G follow", v.scroll)
	}
	return head + "\n" + strings.Join(body, "\n") + "\n" + sDim.Render(" "+hint)
}
