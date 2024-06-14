package modelgateway

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
)

// runFilter feeds an SSE string through filterSSEStream and returns the emitted
// stream + the metered usage.
func runFilter(t *testing.T, sse, sysPrompt string) (string, Usage) {
	t.Helper()
	parse := func(b map[string]any) Usage {
		u := subMap(b, "usage")
		return Usage{InputTokens: num(u, "prompt_tokens"), OutputTokens: num(u, "completion_tokens")}
	}
	var got Usage
	pr, pw := io.Pipe()
	go filterSSEStream(pw, io.NopCloser(strings.NewReader(sse)), sysPrompt, parse, func(u Usage) { got = u })
	out, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("read filtered stream: %v", err)
	}
	return string(out), got
}

// sseContent concatenates all delta.content emitted in an SSE string (a crude
// client model — what LibreChat assembles).
func sseContent(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		p, ok := strings.CutPrefix(ln, "data:")
		if !ok {
			continue
		}
		p = strings.TrimSpace(p)
		if p == "[DONE]" || p == "" {
			continue
		}
		var ch sseChunk
		if json.Unmarshal([]byte(p), &ch) == nil && len(ch.Choices) > 0 {
			b.WriteString(ch.Choices[0].Delta.Content)
		}
	}
	return b.String()
}

func chunk(content, finish string) string {
	if finish != "" {
		return `data: {"choices":[{"delta":{"content":` + strconv.Quote(content) + `},"finish_reason":"` + finish + `"}]}` + "\n\n"
	}
	return `data: {"choices":[{"delta":{"content":` + strconv.Quote(content) + `}}]}` + "\n\n"
}

func TestSSE_MeteringAndCleanPassthrough(t *testing.T) {
	sse := chunk("Hello ", "") + chunk("world", "stop") +
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7}}` + "\n\n" +
		"data: [DONE]\n\n"
