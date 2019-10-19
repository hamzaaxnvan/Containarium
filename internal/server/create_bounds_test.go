package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 3.3 / 3.4 — bounds enforcement.

func TestValidateBounds_AcceptsRealisticRequest(t *testing.T) {
	req := &pb.CreateContainerRequest{
		SshKeys: []string{"ssh-ed25519 AAAA... user@host"},
		StackParameters: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
		Labels: map[string]string{
			"role": "test",
			"team": "platform",
		},
	}
	if err := validateCreateContainerBounds(req); err != nil {
		t.Fatalf("typical small request should pass: %v", err)
	}
}

func TestValidateBounds_RejectsTooManySSHKeys(t *testing.T) {
	keys := make([]string, MaxSSHKeys+1)
	for i := range keys {
		keys[i] = "ssh-ed25519 AAAA..."
	}
	req := &pb.CreateContainerRequest{SshKeys: keys}
	err := validateCreateContainerBounds(req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v (%v)", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), "ssh_keys") {
		t.Fatalf("error should name the offending field: %v", err)
	}
}

func TestValidateBounds_RejectsOversizedSSHKey(t *testing.T) {
	huge := strings.Repeat("A", MaxSSHKeyLength+1)
	req := &pb.CreateContainerRequest{SshKeys: []string{huge}}
