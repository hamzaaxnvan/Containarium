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
	if got := atomic.LoadInt64(&upstreamHits); got != 1 {
		t.Fatalf("upstream hits after first call = %d, want 1", got)
	}

	second := call()
	body, _ := io.ReadAll(second.Body)
	second.Body.Close()

	if second.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("second call status = %d, want 503 (circuit open)", second.StatusCode)
	}
	if got := atomic.LoadInt64(&upstreamHits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 — the denied call reached the provider", got)
	}
	if ra := second.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q, want a positive hint", ra)
	}
	if !strings.Contains(string(body), "circuit open") {
		t.Errorf("body = %q, want it to say why", body)
	}
	// The denial must not leak the credential it was protecting.
	if strings.Contains(string(body), "REAL-KEY") {
		t.Error("denial body contains the provider key")
	}
}

// An operator revoking a tenant takes effect on the next call, with no token
// re-issue and no box restart — the issue's "instantly revocable" requirement.
func TestGateway_RevokeTakesEffectOnAnAlreadyIssuedToken(t *testing.T) {
	secret := []byte("shared-secret")
	var upstreamHits int64
	up := fakeUpstream(t, func(*http.Request) { atomic.AddInt64(&upstreamHits, 1) },
		`{"model":"claude-test","usage":{"input_tokens":1,"output_tokens":1}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{
		Secret: secret, Providers: providers,
		ProviderKeys: map[string]string{"anthropic": "REAL-KEY"},
		Policy:       &PolicyConfig{},
	})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	// A long-lived token, already in a box.
	tok, err := MintToken(secret, GatewayClaims{Tenant: "acme", Provider: "anthropic"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	call := func() int {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages",
			strings.NewReader(`{"model":"claude-test"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	if got := call(); got != http.StatusOK {
		t.Fatalf("pre-revoke status = %d, want 200", got)
	}

	gw.Policy().Revoke("acme", "key suspected compromised")

	if got := call(); got != http.StatusForbidden {
		t.Errorf("post-revoke status = %d, want 403", got)
	}
	if got := atomic.LoadInt64(&upstreamHits); got != 1 {
		t.Errorf("upstream hits = %d, want 1 — a revoked tenant reached the provider", got)
	}

	gw.Policy().Clear("acme")
	if got := call(); got != http.StatusOK {
		t.Errorf("post-clear status = %d, want 200", got)
	}
}

// A gateway with no Policy configured must be exactly what it was before this
// change: unlimited, undetected, unthrottled.
func TestGateway_NoPolicyConfiguredChangesNothing(t *testing.T) {
	secret := []byte("shared-secret")
	var upstreamHits int64
	up := fakeUpstream(t, func(*http.Request) { atomic.AddInt64(&upstreamHits, 1) },
		`{"model":"claude-test","usage":{"input_tokens":100000,"output_tokens":100000}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{Secret: secret, Providers: providers, ProviderKeys: map[string]string{"anthropic": "K"}})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, _ := MintToken(secret, GatewayClaims{Tenant: "acme", Provider: "anthropic"}, time.Hour)
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages",
			strings.NewReader(`{"model":"claude-test"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200 with no policy configured", i, resp.StatusCode)
		}
	}
	if got := atomic.LoadInt64(&upstreamHits); got != 10 {
		t.Errorf("upstream hits = %d, want 10", got)
	}
}

func TestGateway_PolicyEndpointReportsTenantState(t *testing.T) {
	secret := []byte("shared-secret")
	up := fakeUpstream(t, func(*http.Request) {},
		`{"model":"claude-test","usage":{"input_tokens":50,"output_tokens":50}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{
		Secret: secret, Providers: providers,
		ProviderKeys: map[string]string{"anthropic": "K"},
		Policy:       &PolicyConfig{Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 10_000}},
	})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, _ := MintToken(secret, GatewayClaims{Tenant: "acme", SkillID: "s1", Provider: "anthropic"}, time.Hour)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages",
		strings.NewReader(`{"model":"claude-test"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	pr, err := http.Get(srv.URL + "/__gateway/policy")
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Body.Close()
	var got []TenantStatus
	if err := json.NewDecoder(pr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 tenant in the readout, got %d: %+v", len(got), got)
	}
	if got[0].Tenant != "acme" {
		t.Errorf("tenant = %q, want acme", got[0].Tenant)
	}
	if got[0].Tokens != 100 {
		t.Errorf("tokens in window = %d, want 100", got[0].Tokens)
	}

	// The state must serialize as a name, not an integer — an ops readout that
	// says 3 is not an ops readout.
	raw, err := json.Marshal(got[0].State)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"observe"` {
		t.Errorf("state JSON = %s, want \"observe\"", raw)
	}
}

// Anthropic carries the model only in the request body — not the path like
// Gemini, and it is not OpenAI-shaped. Without extracting it there, the
// model-switch signal was silent for the provider skill boxes are provisioned
// for by default, and the lifecycle logs reported an empty model for it.
func TestGateway_ExtractsRequestModelForAnthropic(t *testing.T) {
	secret := []byte("shared-secret")
	var gotBody string
	up := fakeUpstream(t, func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}, `{"model":"claude-sonnet-test","usage":{"input_tokens":5,"output_tokens":5}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{
		Secret: secret, Providers: providers,
		ProviderKeys: map[string]string{"anthropic": "K"},
		Policy:       &PolicyConfig{Anomaly: AnomalyConfig{Enabled: true, NoveltyTTL: time.Hour}},
	})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, _ := MintToken(secret, GatewayClaims{Tenant: "acme", Provider: "anthropic"}, time.Hour)
	body := `{"model":"claude-sonnet-test","max_tokens":16}`
	req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Reading the body to find the model must not consume it.
	if gotBody != body {
		t.Errorf("upstream body = %q, want %q — the body was consumed or rewritten", gotBody, body)
	}

	st := gw.Policy().Status()
	if len(st) != 1 {
		t.Fatalf("want 1 tenant, got %d", len(st))
	}
	if len(st[0].Models) != 1 || st[0].Models[0] != "claude-sonnet-test" {
		t.Errorf("models = %v, want [claude-sonnet-test]", st[0].Models)
	}
}

// Retry-After must round UP. Truncating 1.5s to 1 tells a well-behaved client
// to come back while the throttle is still closed, so it is denied again.
func TestGateway_RetryAfterRoundsUp(t *testing.T) {
	secret := []byte("shared-secret")
	up := fakeUpstream(t, func(*http.Request) {},
		`{"model":"claude-test","usage":{"input_tokens":90,"output_tokens":0}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{
		Secret: secret, Providers: providers,
		ProviderKeys: map[string]string{"anthropic": "K"},
		Policy: &PolicyConfig{
			Quota:               QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
			ThrottleAtQuota:     0.5,
			ThrottleMinInterval: 1500 * time.Millisecond,
		},
	})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, _ := MintToken(secret, GatewayClaims{Tenant: "acme", Provider: "anthropic"}, time.Hour)
	call := func() *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages",
			strings.NewReader(`{"model":"claude-test"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	call() // spends 90 of 100 → past the 50% throttle mark
	call() // admitted, starts the throttle interval
	denied := call()

	if denied.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", denied.StatusCode)
	}
	// The remaining wait is just under 1.5s, so truncation yields 1 and
	// rounding up yields 2.
	if ra := denied.Header.Get("Retry-After"); ra != "2" {
		t.Errorf("Retry-After = %q, want \"2\" (truncation would give \"1\")", ra)
	}
}
