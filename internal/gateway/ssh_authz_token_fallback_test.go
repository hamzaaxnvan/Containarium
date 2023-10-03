package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Token fallback on /v1/ssh/authorize (#2527): the jump host's
// AuthorizedKeysCommand holds a JWT carrying ssh:authorize, never the
// sentinel secret.

const sshAuthzJWTSecret = "ssh-authz-token-fallback-test-secret-0123456789"

func sshAuthzTokenManager(t *testing.T) *auth.TokenManager {
	t.Helper()
	tm, err := auth.NewTokenManager(sshAuthzJWTSecret, "containarium")
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// sshAuthzTokenMux builds the routes as Start does with the fallback on
// (tokenAuth set) or off (nil), the REST mux behind GuardSSHAuthzPath.
func sshAuthzTokenMux(t *testing.T, tokenAuth func(http.Handler) http.Handler, srv pb.SSHAuthzServiceServer) *http.ServeMux {
	t.Helper()
	gw := NewRESTMux()
	if err := pb.RegisterSSHAuthzServiceHandlerServer(context.Background(), gw, srv); err != nil {
		t.Fatal(err)
	}
