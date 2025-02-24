package modelgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recordingResolver is a KeyResolver over a fixed (keyOwner, provider) table
// that also records every lookup, so a test can assert both the key that came
// back AND that a legacy token (no key_owner claim) never reaches the resolver
// at all.
type recordingResolver struct {
	keys  map[string]string // "<keyOwner>|<provider>" -> real key
	calls []string
}

func (r *recordingResolver) KeyFor(_ context.Context, keyOwner, provider string) (string, bool) {
	r.calls = append(r.calls, keyOwner+"|"+provider)
	k, ok := r.keys[keyOwner+"|"+provider]
	return k, ok
}

// newTestServer serves the gateway's handler and closes it with the test.
func newTestServer(t *testing.T, gw *Gateway) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// postModel POSTs one model call through the gateway with tok as the gateway
// credential. The caller closes the response body.
func postModel(t *testing.T, url, tok, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestKeyResolution is the per-owner resolution matrix: (key_owner claim
// present / absent) x (owner key / global key / neither). The claim-absent
// rows are the compatibility golden — a token minted before key_owner existed
// must resolve exactly as it does today, through Config.ProviderKeys, without
// the resolver being consulted at all.
func TestKeyResolution(t *testing.T) {
	tests := []struct {
		name       string
		keyOwner   string            // "" = legacy token, no key_owner claim
		ownerKeys  map[string]string // the KeyResolver's table
		globalKeys map[string]string // Config.ProviderKeys
		noResolver bool
		noFallback bool // Config.DisableGlobalKeyFallback
		wantStatus int
		wantAuth   string   // Authorization the upstream saw
		wantCalls  []string // resolver lookups
	}{
		{
			name:       "legacy token resolves through the global key",
			globalKeys: map[string]string{"openai": "GLOBAL"},
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer GLOBAL",
			wantCalls:  nil,
		},
		{
			name:       "legacy token with no global key is refused",
			wantStatus: http.StatusBadGateway,
			wantCalls:  nil,
		},
		{
			name:       "owner key is used when the claim is present",
			keyOwner:   "org:acme",
			ownerKeys:  map[string]string{"org:acme|openai": "OWNER"},
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer OWNER",
			wantCalls:  []string{"org:acme|openai"},
		},
		{
			name:       "owner key wins over the global key",
			keyOwner:   "org:acme",
			ownerKeys:  map[string]string{"org:acme|openai": "OWNER"},
			globalKeys: map[string]string{"openai": "GLOBAL"},
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer OWNER",
			wantCalls:  []string{"org:acme|openai"},
		},
		{
			name:       "owner with no key falls back to the global key",
			keyOwner:   "org:acme",
			globalKeys: map[string]string{"openai": "GLOBAL"},
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer GLOBAL",
			wantCalls:  []string{"org:acme|openai"},
		},
		{
			name:       "owner with no key and no global key is refused",
			keyOwner:   "org:acme",
			wantStatus: http.StatusBadGateway,
			wantCalls:  []string{"org:acme|openai"},
		},
		{
			name:       "another owner's key is never used",
			keyOwner:   "org:a",
			ownerKeys:  map[string]string{"org:b|openai": "B-KEY"},
			wantStatus: http.StatusBadGateway,
			wantCalls:  []string{"org:a|openai"},
		},
		{
			name:       "claim present but no resolver wired falls back to the global key",
			keyOwner:   "org:acme",
			noResolver: true,
			globalKeys: map[string]string{"openai": "GLOBAL"},
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer GLOBAL",
		},
		{
			name:       "no-fallback: owner key is still used",
			keyOwner:   "org:acme",
			ownerKeys:  map[string]string{"org:acme|openai": "OWNER"},
			globalKeys: map[string]string{"openai": "GLOBAL"},
			noFallback: true,
			wantStatus: http.StatusOK,
			wantAuth:   "Bearer OWNER",
			wantCalls:  []string{"org:acme|openai"},
		},
		{
