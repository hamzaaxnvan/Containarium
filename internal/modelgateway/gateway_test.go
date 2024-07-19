package modelgateway

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a concurrency-safe io.Writer for capturing logger output: the
// gateway writes the END line from its handler goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor polls cond up to ~1s. The END log + lifecycle counters are written
// AFTER the response is flushed to the client (correct: END marks the server
// finishing), so a test that inspects them right after the HTTP call must wait.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func gatewayStatus(t *testing.T, base string) (inflight, completed, failed int64) {
	t.Helper()
	resp, err := http.Get(base + "/__gateway/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct{ Inflight, Completed, Failed int64 }
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st.Inflight, st.Completed, st.Failed
}

// fakeUpstream stands in for the real provider API; it records the auth headers
// it received and returns a canned usage body.
func fakeUpstream(t *testing.T, record func(r *http.Request), body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
}

func TestGateway_Anthropic_KeyInjected_TokenStripped_Metered(t *testing.T) {
	secret := []byte("shared-secret")
	var xKey, auth, gotPath string
	up := fakeUpstream(t, func(r *http.Request) {
		xKey = r.Header.Get("x-api-key")
		auth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
	}, `{"model":"claude-test","usage":{"input_tokens":12,"output_tokens":4,"cache_read_input_tokens":2}}`)
	defer up.Close()

	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	gw := New(Config{Secret: secret, Providers: providers, ProviderKeys: map[string]string{"anthropic": "REAL-KEY"}})
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tok, err := MintToken(secret, GatewayClaims{Tenant: "acme", SkillID: "s1", Provider: "anthropic"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages", strings.NewReader(`{"model":"claude-test"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}

