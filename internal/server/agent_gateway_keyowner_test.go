package server

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/modelgateway"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Skill/crew run gateway tokens carry a key_owner claim (#2134).
//
// Every skill run, crew member and queue worker is provisioned through
// provisionSkillBox, which mints the run's gateway token with
// mintRunGatewayToken. Before #2134 that mint left KeyOwner empty, so every
// run resolved through the daemon-global key (keyowner.go resolveKey case 1)
// no matter which owner had registered a key, and RevokeByKeyOwner could not
// reach a run's token at all.

// ownerKeyResolver is a fake modelgateway.KeyResolver over a fixed
// (keyOwner, provider) table that records every lookup, so a test can assert
// which owner was consulted — and that an unowned run never reaches it.
type ownerKeyResolver struct {
	mu    sync.Mutex
	keys  map[string]string // "<keyOwner>|<provider>" -> real key
	calls []string
}

func (r *ownerKeyResolver) KeyFor(_ context.Context, keyOwner, provider string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, keyOwner+"|"+provider)
	k, ok := r.keys[keyOwner+"|"+provider]
	return k, ok
}

func (r *ownerKeyResolver) lookups() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// runGatewayRig is a real modelgateway.Gateway in front of a fake upstream
// that records the Authorization header it was handed, sharing its HMAC
// secret with an AgentSkillServer's gatewayProvisioning — so a token minted on
// the run path can be spent through the real gateway and the key that reached
// the upstream observed.
type runGatewayRig struct {
	s        *AgentSkillServer
	gw       *modelgateway.Gateway
	resolver *ownerKeyResolver
	url      string

	mu       sync.Mutex
	upAuth   []string
	upstream *httptest.Server
}

func newRunGatewayRig(t *testing.T, ownerKeys map[string]string) *runGatewayRig {
	t.Helper()
	rig := &runGatewayRig{resolver: &ownerKeyResolver{keys: ownerKeys}}
	rig.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.mu.Lock()
		rig.upAuth = append(rig.upAuth, r.Header.Get("Authorization"))
		rig.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(rig.upstream.Close)

	secret := []byte("test-shared-secret")
	providers := modelgateway.DefaultProviders()
	providers["openai"].UpstreamURL = rig.upstream.URL
	rig.gw = modelgateway.New(modelgateway.Config{
		Secret:           secret,
		Providers:        providers,
		ProviderKeys:     map[string]string{"openai": "DAEMON-GLOBAL"},
		KeyResolver:      rig.resolver,
		OwnerRevocations: modelgateway.NewMemOwnerRevocations(),
		Logger:           log.New(io.Discard, "", 0),
	})
	srv := httptest.NewServer(rig.gw.Handler())
	t.Cleanup(srv.Close)
	rig.url = srv.URL + "/v1/model/openai/v1/chat/completions"

	rig.s = &AgentSkillServer{
		gateway: &gatewayProvisioning{httpPort: 8080, secret: secret},
	}
	return rig
}

// call spends tok through the gateway and returns the status and body.
func (rig *runGatewayRig) call(t *testing.T, tok string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rig.url, strings.NewReader(`{"model":"m"}`))
