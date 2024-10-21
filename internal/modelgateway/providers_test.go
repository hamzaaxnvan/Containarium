package modelgateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestNewOpenAICompatibleProvider_Fields pins what the constructor builds: an
// OpenAI-shaped provider at the given upstream, with the conventional key env
// derived from its name, and marked as OpenAI-shaped so the gateway's
// body-buffering / streaming-usage path applies to it.
func TestNewOpenAICompatibleProvider_Fields(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		upstream    string
		wantKeyEnv  string
		wantUpsteam string
	}{
		{name: "single word", provider: "acmeai", upstream: "https://region-a.example.com", wantKeyEnv: "ACMEAI_API_KEY", wantUpsteam: "https://region-a.example.com"},
		{name: "hyphenated", provider: "my-vendor", upstream: "https://region-a.example.com/", wantKeyEnv: "MY_VENDOR_API_KEY", wantUpsteam: "https://region-a.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewOpenAICompatibleProvider(tc.provider, tc.upstream)
			if p.Name != tc.provider {
				t.Errorf("Name = %q, want %q", p.Name, tc.provider)
			}
			if p.UpstreamURL != tc.wantUpsteam {
				t.Errorf("UpstreamURL = %q, want %q (trailing slash must be trimmed)", p.UpstreamURL, tc.wantUpsteam)
			}
			if p.KeyEnv != tc.wantKeyEnv {
				t.Errorf("KeyEnv = %q, want %q", p.KeyEnv, tc.wantKeyEnv)
			}
			if !p.openAIShaped {
				t.Error("provider is not marked OpenAI-shaped; the gateway's streaming-usage path would skip it")
			}
		})
	}
}

// TestNewOpenAICompatibleProvider_Injection — the real key goes upstream as a
// bearer token and every inbound gateway credential shape is stripped, so
// neither the gateway token nor a client-supplied key ever reaches the
// upstream.
func TestNewOpenAICompatibleProvider_Injection(t *testing.T) {
	secret := []byte("s")
	var gotAuth, gotXAPIKey, gotXGoog, gotPath string
	up := fakeUpstream(t, func(r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotXAPIKey = r.Header.Get("X-Api-Key")
		gotXGoog = r.Header.Get("X-Goog-Api-Key")
		gotPath = r.URL.Path
	}, `{"model":"vendor-small","usage":{"prompt_tokens":11,"completion_tokens":5}}`)
	defer up.Close()

