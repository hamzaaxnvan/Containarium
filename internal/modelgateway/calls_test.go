package modelgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// callsFixture is a gateway in front of an upstream that answers with status,
// recording into a CallLog.
func callsFixture(t *testing.T, status int, cfg func(*Config)) (srv *httptest.Server, log *CallLog, secret []byte) {
	t.Helper()
	secret = []byte("shared-secret")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"x"}`)
	}))
	t.Cleanup(up.Close)
	providers := DefaultProviders()
	providers["anthropic"].UpstreamURL = up.URL
	log = NewCallLog()
	c := Config{Secret: secret, Providers: providers, ProviderKeys: map[string]string{"anthropic": "OPERATOR-KEY-SECRET"}, Calls: log}
	if cfg != nil {
		cfg(&c)
	}
	srv = httptest.NewServer(New(c).Handler())
	t.Cleanup(srv.Close)
	return srv, log, secret
}

func callOnce(t *testing.T, srv *httptest.Server, secret []byte, claims GatewayClaims) int {
	t.Helper()
	tok, err := MintToken(secret, claims, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/v1/model/anthropic/v1/messages", strings.NewReader(`{"model":"claude-test","messages":[{"role":"user","content":"PROMPT-SECRET"}]}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// The acceptance case: an upstream 401 on the operator's key produces exactly
// one event, with the right status and owner type, and nothing secret in it.
func TestCallLog_Upstream401IsOneEventWithOwnerType(t *testing.T) {
	srv, log, secret := callsFixture(t, 401, nil)
	if got := callOnce(t, srv, secret, GatewayClaims{Tenant: "acme", SkillID: "s1", RunID: "run-1", Provider: "anthropic"}); got != 401 {
		t.Fatalf("status %d", got)
	}
	var calls []*pb.GatewayCall
	waitFor(t, "the call event", func() bool { calls, _ = log.Since("run-1", "", 0); return len(calls) > 0 })
	if len(calls) != 1 {
		t.Fatalf("want exactly one event, got %d", len(calls))
	}
	c := calls[0]
	if c.Status != 401 || c.TransportError != pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_UNSPECIFIED {
		t.Errorf("status/transport = %d/%v", c.Status, c.TransportError)
	}
	if c.KeyOwner != pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_OPERATOR_FALLBACK || c.Provider != "anthropic" || c.Model != "claude-test" || c.SkillId != "s1" {
		t.Errorf("event = %v", c)
	}
	// No secret can be in the event: it has no field for one. Prove it on the wire form.
	b, _ := protojson.Marshal(c)
	for _, secretText := range []string{"OPERATOR-KEY-SECRET", "PROMPT-SECRET", "shared-secret"} {
		if strings.Contains(string(b), secretText) {
			t.Errorf("event leaks %q: %s", secretText, b)
		}
	}
}

func TestCallLog_KeyOwnerTypeComesFromTheNamespace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
		keys  map[string]string // owner -> key the resolver has
		want  pb.GatewayKeyOwner
	}{
		{"org key", "org:o1", map[string]string{"org:o1": "K"}, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_ORG},
		{"member key", "user:alice", map[string]string{"user:alice": "K"}, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_MEMBER},
		{"owner without a key falls back to the operator's", "org:o2", nil, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_OPERATOR_FALLBACK},
		{"no owner on the token", "", nil, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_OPERATOR_FALLBACK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, log, secret := callsFixture(t, 200, func(c *Config) {
				c.KeyResolver = KeyResolverFunc(func(_ context.Context, owner, _ string) (string, bool) {
					k, ok := tc.keys[owner]
					return k, ok
