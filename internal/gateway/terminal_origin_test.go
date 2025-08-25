package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/gorilla/websocket"
)

func newOriginRequest(origin string, mutate func(*http.Request)) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/containers/alice/terminal", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

// markValidated does what the gateway's terminal wrapper does after
// ValidateToken succeeds: record the validated bearer's source on the
// request context. It validates nothing itself.
func markValidated(r *http.Request) *http.Request {
	_, src := auth.ExtractBearerForUpgrade(r)
	return r.WithContext(withValidatedBearer(r.Context(), src))
}

func withSubprotocolBearer(r *http.Request) {
	r.Header.Set("Sec-WebSocket-Protocol", auth.WSSubprotocolBearer+", header.payload.sig")
}

func withAuthorizationBearer(r *http.Request) {
	r.Header.Set("Authorization", "Bearer header.payload.sig")
}

func TestCheckTerminalOrigin(t *testing.T) {
	t.Setenv("CONTAINARIUM_ALLOWED_ORIGINS", "")

	tests := []struct {
		name       string
		req        *http.Request
		wantOK     bool
		wantReason string
	}{
		{"empty origin + validated subprotocol bearer upgrades", markValidated(newOriginRequest("", withSubprotocolBearer)), true, ""},
		{"empty origin + validated Authorization bearer upgrades", markValidated(newOriginRequest("", withAuthorizationBearer)), true, ""},
		{"empty origin + subprotocol bearer never validated refused", newOriginRequest("", withSubprotocolBearer), false, originRequired},
		{"empty origin + Authorization bearer never validated refused", newOriginRequest("", withAuthorizationBearer), false, originRequired},
		{"empty origin + no bearer refused", markValidated(newOriginRequest("", nil)), false, originRequired},
		{"empty origin + only validated ?token= refused", markValidated(newOriginRequest("", func(r *http.Request) {
			r.URL.RawQuery = "token=header.payload.sig"
		})), false, originRequired},
		{"unlisted origin + validated bearer refused", markValidated(newOriginRequest("https://evil.example", withSubprotocolBearer)), false, originNotAllowed},
		{"unlisted origin, no bearer refused", newOriginRequest("https://evil.example", nil), false, originNotAllowed},
		{"allowed origin + validated bearer upgrades", markValidated(newOriginRequest("http://localhost:3000", withSubprotocolBearer)), true, ""},
