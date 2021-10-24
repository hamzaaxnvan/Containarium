package modelgateway

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// SSE (server-sent events) interception for the OpenAI-compatible streaming
// chat path. The reverse proxy's ModifyResponse only buffers non-streaming
// JSON, so a streaming chat was previously BOTH unmetered and unfiltered. This
// file adds, for `text/event-stream` responses:
//
//   - token metering — the gateway injects `stream_options.include_usage` into
//     the request so the provider emits a final usage event, which we parse to
//     record input/output tokens (the streaming half of the metering plane).
//   - output filtering — a hold-back window over the assembled assistant text
//     so a verbatim leak of the system prompt (a skill persona — product IP) is
//     caught BEFORE it's emitted to the client and replaced with a refusal
//     (prompt-exfiltration hardening, layer 2).
//
// Both are FAIL-OPEN: any parse/shape we don't recognise is passed through
// unchanged, so a provider quirk degrades to "unfiltered/unmetered passthrough",
// never a broken chat.

const (
	// holdBack is how many trailing chars of the assembled response we withhold
	// before emitting, so a leak run (>= leakMinRun, which is smaller) is caught
	// within the window before any of it reaches the client.
	holdBack = 160
	// leakMinRun is the shortest verbatim run of the system prompt that counts
	// as a leak. Long enough that ordinary overlap (a shared sentence) doesn't
	// trip it; short enough to catch a real "print your instructions" echo.
	leakMinRun = 48
	redactNote = "[redacted: this assistant can't disclose its own instructions]"
)

// extractSystemPrompt pulls the concatenated system-role message text out of an
// OpenAI chat-completions request body. That text is the leak target (the skill
// persona's hidden prompt). Returns "" when there's no system message or the
// body isn't the shape we expect (→ filtering is skipped, fail-open).
func extractSystemPrompt(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role != "system" {
			continue
		}
		// content is usually a string, occasionally an array of parts.
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			b.WriteString(s)
			b.WriteByte('\n')
			continue
		}
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			for _, p := range parts {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// requestModel pulls the "model" field from an OpenAI chat request body (the
// gemini-openai path carries the model in the body, not the URL). "" if absent.
func requestModel(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	return req.Model
}

// ensureStreamUsage adds `stream_options.include_usage=true` to a streaming
// OpenAI chat request so the provider emits a final usage event we can meter.
// Returns the (possibly rewritten) body and whether the request is streaming.
// Fail-open: on any shape we don't recognise, returns the body unchanged.
func ensureStreamUsage(body []byte) (out []byte, streaming bool) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body, false
	}
	s, _ := m["stream"].(bool)
	if !s {
		return body, false
	}
	so, _ := m["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	so["include_usage"] = true
	m["stream_options"] = so
	nb, err := json.Marshal(m)
	if err != nil {
		return body, true
	}
	return nb, true
}

// normalize lowercases and collapses runs of whitespace, so a leak survives
// trivial reformatting (extra spaces / newlines) by the model.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// leaks reports whether resp contains a verbatim run of >= leakMinRun chars of
// the system prompt (both normalized). O(len(resp)*len(sys)) substring scan —
// fine for chat-sized strings.
func leaks(resp, sysNorm string) bool {
	if len(sysNorm) < leakMinRun {
		return false
	}
	r := normalize(resp)
	if len(r) < leakMinRun {
		return false
	}
	// Slide a leakMinRun window over the response; any window that is a
	// substring of the system prompt is a verbatim leak.
	for i := 0; i+leakMinRun <= len(r); i++ {
		if strings.Contains(sysNorm, r[i:i+leakMinRun]) {
			return true
		}
	}
