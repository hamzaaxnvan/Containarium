package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var sshAuthzSecret = []byte(strings.Repeat("s", auth.SentinelMinSecretLen))

// jwtFallbackBody stands in for the "/v1/" JWT chain's refusal, so a test
// can tell the sentinel mount's 401 from a fall-through.
const jwtFallbackBody = `{"error":"jwt chain","code":401}`

// sshAuthzMux builds the routes the way Start does: a "/v1/" catch-all that
// is not the sentinel mount, then MountSSHAuthz over the production REST
// mux serving srv.
func sshAuthzMux(t *testing.T, v auth.SentinelVerifier, srv pb.SSHAuthzServiceServer) *http.ServeMux {
	t.Helper()
	gw := NewRESTMux()
	if err := pb.RegisterSSHAuthzServiceHandlerServer(context.Background(), gw, srv); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	httpMux := http.NewServeMux()
	httpMux.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, jwtFallbackBody, http.StatusUnauthorized)
	}))
	MountSSHAuthz(httpMux, v, nil, gw)
	return httpMux
}

func newSSHAuthzRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, SSHAuthzAuthorizePath, strings.NewReader(`{"login":"alice"}`))
}

func serve(h http.Handler, req *http.Request) (int, string) {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	return rr.Code, string(body)
}

func TestSSHAuthzMount_SignedRequestReachesStub(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		v    auth.SentinelVerifier
		sign func(*http.Request)
	}{
		{"hmac", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(r *http.Request) { auth.SignSentinelRequest(r, sshAuthzSecret) }},
		{"ed25519", auth.NewSentinelVerifier(pub, nil), func(r *http.Request) { auth.SignSentinelRequestEd25519(r, priv) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newSSHAuthzRequest()
			tt.sign(req)
			code, body := serve(sshAuthzMux(t, tt.v, unimplementedAuthz{}), req)
			if code != http.StatusNotImplemented {
				t.Fatalf("code = %d body = %s, want 501 from the Unimplemented stub", code, body)
			}
		})
	}
}

func TestSSHAuthzMount_UnverifiedRequestIs401AtTheMount(t *testing.T) {
	tests := []struct {
		name string
		v    auth.SentinelVerifier
		mut  func(*http.Request)
	}{
		{"unsigned", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(*http.Request) {}},
		{"wrong secret", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(r *http.Request) {
			auth.SignSentinelRequest(r, []byte(strings.Repeat("x", auth.SentinelMinSecretLen)))
		}},
		{"stale timestamp", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(r *http.Request) {
			auth.SignSentinelRequest(r, sshAuthzSecret)
			r.Header.Set(auth.SentinelHeaderTimestamp, "1000000000")
		}},
		{"signed for another path", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(r *http.Request) {
			r.URL.Path = AnonDoorEnsurePath
			auth.SignSentinelRequest(r, sshAuthzSecret)
			r.URL.Path = SSHAuthzAuthorizePath
		}},
		{"verifier unconfigured", auth.NewSentinelVerifier(nil, nil), func(r *http.Request) { auth.SignSentinelRequest(r, sshAuthzSecret) }},
		// A bearer token is not a way in: this route has no JWT fallback.
		{"bearer only", auth.NewSentinelVerifier(nil, sshAuthzSecret), func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newSSHAuthzRequest()
			tt.mut(req)
			code, body := serve(sshAuthzMux(t, tt.v, unimplementedAuthz{}), req)
			if code != http.StatusUnauthorized {
				t.Fatalf("code = %d body = %s, want 401", code, body)
			}
