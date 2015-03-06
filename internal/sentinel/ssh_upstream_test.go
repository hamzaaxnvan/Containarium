package sentinel

import (
	"context"
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSSHUpstreamUsesAuthoritativeEndpointIdentity respects advertised ingress.
func TestSSHUpstreamUsesAuthoritativeEndpointIdentity(t *testing.T) {
	ks := NewKeyStore()
	ks.backends["direct"] = &backendKeys{backendID: "direct", backendIP: "192.0.2.1", sshPort: 32022}
	ks.backends["tunnel"] = &backendKeys{backendID: "tunnel", backendIP: "127.0.0.10", sshPort: 22}
	for endpoint, want := range map[string]string{"192.0.2.1:32022": "direct", "127.0.0.10:20022": "tunnel"} {
		if got, err := ks.BackendForEndpoint(endpoint); err != nil || got != want {
			t.Fatalf("route %s: %q %v", endpoint, got, err)
		}
	}
	if _, err := ks.BackendForEndpoint("192.0.2.1:22"); err == nil {
		t.Fatal("unadvertised port became an enforcement route")
	}
	ks.RemoveBackend("direct")
	if _, err := ks.BackendForEndpoint("192.0.2.1:32022"); err == nil {
		t.Fatal("removed backend remained authoritative")
	}
}

// TestSSHUpstreamSupervisorRecoversStartup retries after an operator repairs config.
func TestSSHUpstreamSupervisorRecoversStartup(t *testing.T) {
	dir, err := os.MkdirTemp("", "sshsupervisor-")
