package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/agentengine"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2125: POST /tasks on a box's in-box A2A server is a daemon-only endpoint.
// The daemon authenticates with the credential it derived for THAT box; the
// box refuses anything else. These tests cover the daemon's half — that it
// sends the right box's credential, that it never sends a task it cannot
// authenticate, and that a peer's refusal is not reported as a network fault.
// The receiving half (401/403 before engine or journal) is
// agent-runtime/src/a2a.test.ts.

const testA2AJWTSecret = "test-jwt-secret-long-enough-for-hmac-sha256-0123456789"

func a2aTestServer(t *testing.T) *AgentSkillServer {
	t.Helper()
	tm, err := auth.NewTokenManager(testA2AJWTSecret, "containarium-test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return &AgentSkillServer{tokens: tm}
}

// Each box gets its own credential, stable across calls (the daemon recomputes
// rather than stores it) — so a box holding its own cannot use it at a peer.
func TestAgentA2ASecret_PerBoxAndStable(t *testing.T) {
	s := a2aTestServer(t)

	hello := s.agentA2ASecret("hello-agent")
	if hello == "" {
		t.Fatal("no credential derived for hello-agent")
	}
	if again := s.agentA2ASecret("hello-agent"); again != hello {
		t.Errorf("credential is not stable: %q then %q", hello, again)
	}
	if relay := s.agentA2ASecret("relay-agent"); relay == hello {
		t.Errorf("relay-agent derives hello-agent's credential %q", hello)
	}
	// A second daemon with a different signing key cannot open this box.
	otherTM, err := auth.NewTokenManager(testA2AJWTSecret+"-rotated", "containarium-test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
