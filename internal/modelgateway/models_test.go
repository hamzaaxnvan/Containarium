package modelgateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Upstream model listing through the gateway (#1726). The point of the verb is
// that the CALLER never holds a real key: it names an owner and a provider, and
// the gateway spends that owner's stored key exactly as the data plane would.

// fakeModelsUpstream serves an OpenAI-shaped /v1/models and records the
// Authorization header it was called with.
func fakeModelsUpstream(t *testing.T, body string, code int) (*httptest.Server, *string) {
	t.Helper()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("upstream path = %q, want /v1/models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotAuth
}

func TestGatewayListModels_SpendsTheOwnersKey(t *testing.T) {
	up, gotAuth := fakeModelsUpstream(t, `{"object":"list","data":[{"id":"m-small"},{"id":"m-large"}]}`, http.StatusOK)

	g := New(Config{
		Logger:    log.New(io.Discard, "", 0),
		Providers: map[string]*Provider{"acmeai": NewOpenAICompatibleProvider("acmeai", up.URL)},
		KeyResolver: KeyResolverFunc(func(_ context.Context, keyOwner, provider string) (string, bool) {
			if keyOwner == OrgKeyOwner("acme") && provider == "acmeai" {
				return "sk-owner", true
			}
			return "", false
		}),
	})

	models, err := g.ListModels(context.Background(), OrgKeyOwner("acme"), "acmeai")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0] != "m-small" || models[1] != "m-large" {
