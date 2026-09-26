package cockpit

import (
	"strings"
	"testing"
)

func TestParseTranscript(t *testing.T) {
	data := strings.Join([]string{
		`{"type":"user","message":{"content":"Review the diff"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Looking."},{"type":"tool_use","name":"Bash","input":{"command":"git diff\nHEAD"}}],"stop_reason":"tool_use"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"+ added"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"TODO","path":"src"}}]}}`,
		`not json`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"All good."}],"stop_reason":"end_turn"}}`,
	}, "\n")
	evs, done := parseTranscript([]byte(data))
	var got []string
	for _, e := range evs {
		got = append(got, e.kind+":"+e.text)
	}
	want := "prompt:Review the diff|text:Looking.|tool:Bash(git diff HEAD)|result:+ added|tool:Grep(TODO)|text:All good."
	if strings.Join(got, "|") != want || !done {
		t.Fatalf("done=%v\n got %s\nwant %s", done, strings.Join(got, "|"), want)
	}
}
