package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The load-bearing property of the whole feature: a request the ladder refuses
// must never reach the provider. The real key is injected in the proxy's
// Director, so "denied" has to mean the Director never runs — not that we
// discarded the response afterwards. Asserting on upstream hits proves it in
// the only way that can't quietly regress.
func TestGateway_DeniedRequestNeverReachesUpstream(t *testing.T) {
	secret := []byte("shared-secret")
	var upstreamHits int64
	var sawKey atomic.Value
	sawKey.Store("")

	up := fakeUpstream(t, func(r *http.Request) {
		atomic.AddInt64(&upstreamHits, 1)
		sawKey.Store(r.Header.Get("x-api-key"))
	}, `{"model":"claude-test","usage":{"input_tokens":900,"output_tokens":900}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{
		Secret:       secret,
		Providers:    providers,
		ProviderKeys: map[string]string{"anthropic": "REAL-KEY"},
		Policy: &PolicyConfig{
			// One call's worth of budget: the first request goes through and
			// spends 1800 tokens, the second finds the budget gone.
			Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 1000},
		},
	})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, err := MintToken(secret, GatewayClaims{Tenant: "acme", SkillID: "s1", Provider: "anthropic"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := func() *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages",
			strings.NewReader(`{"model":"claude-test"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := call()
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", first.StatusCode)
	}
