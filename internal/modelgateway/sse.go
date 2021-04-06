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
	return false
}

// sseChunk is the slice of an OpenAI streaming chunk we read. The top-level
// envelope fields (id/object/created/model) are captured so the re-serialized
// content chunks we emit carry the SAME envelope as the upstream — a chunk
// missing object/id/model is rejected by strict clients (LibreChat v0.8.6 hangs
// on it), so the filtered path must reproduce the full shape, not a minimal one.
type sseChunk struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created json.RawMessage `json:"created"`
	Model   string          `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

// outChunk is an OpenAI-shaped streaming chunk the gateway re-emits on the
// filtered path (held-back content, redaction note, finish/usage envelope). It
// mirrors the upstream envelope so strict clients accept it. Usage is kept as
// raw JSON: it is the provider's own pass-through metering object (the
// type-erasing wire boundary), forwarded verbatim, never constructed here.
type outChunk struct {
	ID      string          `json:"id,omitempty"`
	Object  string          `json:"object"`
	Created json.RawMessage `json:"created,omitempty"`
	Model   string          `json:"model,omitempty"`
	Choices []outChoice     `json:"choices,omitempty"`
	Usage   json.RawMessage `json:"usage,omitempty"`
}

type outChoice struct {
	Index        int      `json:"index"`
	Delta        outDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type outDelta struct {
	Content string `json:"content,omitempty"`
}

// chunkHasToolCalls reports whether a streaming choice carries a non-empty
// tool_calls array — the delta that asks the client to invoke a function.
func chunkHasToolCalls(ch sseChunk) bool {
	if len(ch.Choices) == 0 {
		return false
	}
	raw := strings.TrimSpace(string(ch.Choices[0].Delta.ToolCalls))
	return raw != "" && raw != "null" && raw != "[]"
}

// normalizeToolFinish rewrites finish_reason "stop" -> "tool_calls" in an
// OpenAI chat chunk payload. Gemini's OpenAI-compat surface returns "stop" even
// when it emitted a tool call, which is NOT OpenAI-conformant: an agent client
// (e.g. LibreChat) then never runs the tool and the turn hangs until reaped.
// Only choices whose finish_reason is exactly "stop" are touched; the tool_calls
// themselves and any provider extras are preserved. Fail-open: returns the input
// unchanged on any shape it doesn't recognise or if nothing changed.
func normalizeToolFinish(payload string) string {
	var m map[string]any
	if json.Unmarshal([]byte(payload), &m) != nil {
		return payload
	}
	chs, ok := m["choices"].([]any)
	if !ok {
		return payload
	}
	changed := false
	for _, c := range chs {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if fr, _ := cm["finish_reason"].(string); fr == "stop" {
			cm["finish_reason"] = "tool_calls"
			changed = true
		}
	}
	if !changed {
		return payload
	}
	nb, err := json.Marshal(m)
	if err != nil {
		return payload
	}
	return string(nb)
}

// standardizeToolCalls rewrites an OpenAI streaming chunk's tool_calls deltas to
// the spec shape a LangChain-based client (LibreChat v0.8.6) requires: each
// entry MUST carry an `index` (used to merge streamed tool-call deltas) and only
// the standard fields. Gemini's OpenAI-compat surface OMITS `index` and adds a
// non-standard `extra_content` (a `thought_signature`); without the index the
// client's stream parser throws and aborts the turn ("terminated") before it can
// run the tool, and the stray field can trip strict decoders. We inject a
// position-based index where missing and drop `extra_content`. Fail-open:
// returns the input unchanged on any shape it doesn't recognise or no change.
func standardizeToolCalls(payload string) string {
	var m map[string]any
	if json.Unmarshal([]byte(payload), &m) != nil {
		return payload
	}
	chs, ok := m["choices"].([]any)
	if !ok {
		return payload
	}
	changed := false
	for _, c := range chs {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		delta, _ := cm["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		tcs, _ := delta["tool_calls"].([]any)
		for i, tc := range tcs {
			tcm, _ := tc.(map[string]any)
			if tcm == nil {
				continue
			}
			if _, has := tcm["index"]; !has {
				tcm["index"] = i
				changed = true
			}
			if _, has := tcm["extra_content"]; has {
				delete(tcm, "extra_content")
				changed = true
			}
		}
	}
