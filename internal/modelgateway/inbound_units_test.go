package modelgateway

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Scan-unit extraction for the inbound guardrail (#2367 slice B2). Every
// fixture is synthetic and in the real provider wire shape. The property
// under test: every text part and every tool call's name plus argument
// string becomes a unit, arguments split across streamed chunks are
// reassembled, and a shape nothing recognises is scanned whole rather than
// not at all.

func unitMap(units []*pb.GuardrailTextUnit) map[string]string {
	m := map[string]string{}
	for _, u := range units {
		m[u.GetUnitId()] = u.GetText()
	}
	return m
}

func TestInboundUnitsJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
		want map[string]string
	}{
		{"anthropic text and tool_use",
			`{"type":"message","role":"assistant","content":[{"type":"text","text":"Running it."},{"type":"tool_use","id":"toolu_1","name":"bash","input":{"command":"ls -la"}}]}`,
			map[string]string{"text/0": "Running it.", "tool/1": "bash\n{\"command\":\"ls -la\"}"}},
		{"openai string content and tool_calls",
			`{"choices":[{"index":0,"message":{"role":"assistant","content":"Done.","tool_calls":[{"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
			map[string]string{"text/0": "Done.", "tool/0/0": "write_file\n{\"path\":\"a.go\"}"}},
		{"openai content parts",
			`{"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"part one "},{"type":"text","text":"part two"}]}}]}`,
			map[string]string{"text/0": "part one part two"}},
		{"gemini text and functionCall parts",
			`{"candidates":[{"content":{"parts":[{"text":"Sure."},{"functionCall":{"name":"run_shell","args":{"cmd":"make test"}}}],"role":"model"}}]}`,
			map[string]string{"text/0": "Sure.", "tool/0/1": "run_shell\n{\"cmd\":\"make test\"}"}},
		{"unrecognised shape is scanned whole",
			`{"error":{"type":"overloaded_error","message":"try again"}}`,
			map[string]string{"body": `{"error":{"type":"overloaded_error","message":"try again"}}`}},
		{"not JSON is scanned whole",
			`<html>upstream said no</html>`,
			map[string]string{"body": `<html>upstream said no</html>`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unitMap(inboundUnitsJSON([]byte(tc.body)))
			if len(got) != len(tc.want) {
				t.Fatalf("units = %v, want %v", got, tc.want)
			}
			for id, text := range tc.want {
				if got[id] != text {
					t.Errorf("unit %q = %q, want %q", id, got[id], text)
				}
			}
		})
	}
