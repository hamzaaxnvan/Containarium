package modelgateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Scan units for the inbound guardrail: what a model response would write
// into a box. A coding CLI acts on assistant text and, above all, on tool
// calls (edit, write, shell), so the units are every text part and every
// tool call's name plus its argument string, reassembled across streamed
// chunks (docs/architecture/guardrail-inbound-and-server-policy.md, "What
// gets scanned"). Three wire shapes are recognised: OpenAI chat completions
// (and every OpenAI-shaped upstream), Anthropic messages, and Gemini's
// native generateContent. A body in none of these shapes is scanned whole,
// as one unit, so an unrecognised shape is never an unscanned one.

// unitBuilder accumulates text per unit key in first-seen order.
type unitBuilder struct {
	order []string
	parts map[string]*strings.Builder
	names map[string]string // tool-call name per tool unit key
}

func newUnitBuilder() *unitBuilder {
	return &unitBuilder{parts: map[string]*strings.Builder{}, names: map[string]string{}}
}

func (b *unitBuilder) add(key, s string) {
	sb, ok := b.parts[key]
	if !ok {
		sb = &strings.Builder{}
		b.parts[key] = sb
		b.order = append(b.order, key)
	}
	sb.WriteString(s)
}

func (b *unitBuilder) name(key, name string) {
	if name != "" {
		b.names[key] = name
		b.add(key, "") // register the unit even before any arguments arrive
	}
}

// units renders the accumulated parts. With nothing recognised it returns the
// whole raw body as one unit, keyed fallbackID.
func (b *unitBuilder) units(fallbackID string, raw []byte) []*pb.GuardrailTextUnit {
	if len(b.order) == 0 {
		return []*pb.GuardrailTextUnit{{UnitId: fallbackID, Text: string(raw)}}
	}
	out := make([]*pb.GuardrailTextUnit, 0, len(b.order))
	for _, k := range b.order {
		text := b.parts[k].String()
		if n, ok := b.names[k]; ok {
			text = n + "\n" + text
		}
		out = append(out, &pb.GuardrailTextUnit{UnitId: k, Text: text})
	}
	return out
}

// --- OpenAI chat completions (non-streaming message, streaming delta) ---

type oaiToolCall struct {
	Index    *int `json:"index"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiMessage struct {
	Content   json.RawMessage `json:"content"` // string, or an array of {text} parts
	ToolCalls []oaiToolCall   `json:"tool_calls"`
}

type oaiBody struct {
	Choices []struct {
		Index   int        `json:"index"`
		Message oaiMessage `json:"message"`
		Delta   oaiMessage `json:"delta"`
	} `json:"choices"`
}

// textOf reads a content field that is either a JSON string or an array of
// parts with a text field.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
