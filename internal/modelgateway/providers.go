package modelgateway

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Provider describes one upstream model API the gateway brokers. The two
// engines the agent-runtime ships (Anthropic, OpenAI) plus Gemini (the cheap
// test engine) match the agent-runtime's three engines.
type Provider struct {
	Name        string
	UpstreamURL string // scheme://host, no trailing slash
	KeyEnv      string // env var the gateway reads the REAL provider key from

	// inject sets the upstream's auth header from the real provider key and
	// strips any inbound gateway credential, so the gateway token never leaks
	// upstream and the real key never came from the box.
	inject func(h http.Header, key string)
	// parseUsage extracts token usage + model from a decoded JSON response.
	// pathModel is the model id recovered from the request path (Gemini puts
	// the model in the URL, not the response body).
	parseUsage func(body map[string]any, pathModel string) Usage

	// openAIShaped marks a provider that speaks OpenAI's chat-completions
	// protocol: the model is in the request body, the usage block is
	// OpenAI-shaped, and `stream_options.include_usage` is what makes a
	// streamed response meterable. handleModel keys its request-body handling
	// off this instead of a hard-coded list of provider names, so a provider an
	// operator registers at runtime (ProvidersFromEnv) gets the same treatment
	// as the compiled-in ones.
	openAIShaped bool
}

// Usage is the metered token counts for one model call.
type Usage struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
}

func stripGatewayAuth(h http.Header) {
	h.Del("Authorization")
	h.Del("X-Api-Key")
	h.Del("X-Goog-Api-Key")
}

func num(m map[string]any, k string) int64 {
