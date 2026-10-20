package sentinel

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// Regression coverage for Phase 0.6 (finding C-CRIT-2): the
// sentinel's /sentinel/peers response must be HMAC-signed so a
// compromised network path can't inject attacker peer URLs that the
// daemon would then proxy container traffic through.

const peersTestSecret = "abcdefghijklmnopqrstuvwxyz0123456789ABCD" // 40 bytes

func newManagerForPeersTest(secret string) *Manager {
	m := &Manager{backends: NewBackendPool()}
	if secret != "" {
		m.SetHMACSecret([]byte(secret))
	}
	m.backends.Add(&Backend{ID: "tunnel-a", Type: BackendTunnel, Healthy: true, Pool: "prod"})
	m.backends.Add(&Backend{ID: "tunnel-b", Type: BackendTunnel, Healthy: false, Pool: "prod"})
	return m
}

func TestPeersHandler_SignsResponse(t *testing.T) {
	m := newManagerForPeersTest(peersTestSecret)

	req := httptest.NewRequest("GET", "/sentinel/peers", nil)
	rec := httptest.NewRecorder()
	m.PeersHandler()(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get(auth.SentinelHeaderSignature) == "" {
		t.Fatal("response missing signature header")
	}
	if rec.Header().Get(auth.SentinelHeaderTimestamp) == "" {
		t.Fatal("response missing timestamp header")
	}

	// The signature must verify against the exact bytes the daemon
	// will see. If the handler ever started double-encoding (e.g.
	// json.Encoder appends a trailing newline) the daemon's verify
	// would silently fail; this test catches that drift.
	resp := rec.Result()
	body := rec.Body.Bytes()
	if err := auth.VerifySentinelResponse(resp, []byte(peersTestSecret), body, time.Now()); err != nil {
		t.Fatalf("daemon-side verify failed: %v", err)
	}

	// And the body itself still parses to the same shape the
	// existing pool test expects — the signing shouldn't change the
	// wire format.
	var parsed struct {
		Peers []testPeer `json:"peers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if len(parsed.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(parsed.Peers))
	}
}

func TestPeersHandler_NoSecret_NoSignature(t *testing.T) {
	// Unconfigured sentinel: do not write fake headers. The daemon
	// then knows to fail-closed (or log the rollout warning).
	m := newManagerForPeersTest("")

	req := httptest.NewRequest("GET", "/sentinel/peers", nil)
	rec := httptest.NewRecorder()
	m.PeersHandler()(rec, req)

	if rec.Header().Get(auth.SentinelHeaderSignature) != "" {
		t.Fatal("unconfigured sentinel must not write signature header")
	}
}

func TestPeersHandler_ShortSecret_NoSignature(t *testing.T) {
	m := newManagerForPeersTest("short")

	req := httptest.NewRequest("GET", "/sentinel/peers", nil)
	rec := httptest.NewRecorder()
	m.PeersHandler()(rec, req)

	if rec.Header().Get(auth.SentinelHeaderSignature) != "" {
		t.Fatal("short-secret sentinel must not write signature header")
	}
}

func TestPeersHandler_TamperingBreaksSignature(t *testing.T) {
	// End-to-end: legitimate sentinel signs a response; an
	// in-the-middle attacker swaps a peer's proxy_path. The daemon
	// should reject the tampered body even though the signature
	// header looks valid.
	m := newManagerForPeersTest(peersTestSecret)

	req := httptest.NewRequest("GET", "/sentinel/peers", nil)
	rec := httptest.NewRecorder()
	m.PeersHandler()(rec, req)
	resp := rec.Result()

	tampered := []byte(strings.Replace(rec.Body.String(),
		`"proxy_path":"/peer/tunnel-a"`, `"proxy_path":"/peer/attacker"`, 1))
	if err := auth.VerifySentinelResponse(resp, []byte(peersTestSecret), tampered, time.Now()); err == nil {
		t.Fatal("verifier accepted a tampered peer list — signing layer is broken")
	}
}
