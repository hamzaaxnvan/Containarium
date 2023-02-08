package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Birth TTL (#523): a box should be born with a death date so it can't leak
// when the separate `ttl set` call never runs. These tests cover the
// create→persist path (stampBirthTTL) and the validation it shares with
// SetContainerTTL (validateTTLSeconds), mirroring container_server_ttl_test.go.

// TestStampBirthTTL_StampsExpiry — the success path writes one SetConfig with
// an RFC3339 value ~duration in the future, using the SAME key the sweeper
// reads, and does NOT delete the box.
func TestStampBirthTTL_StampsExpiry(t *testing.T) {
	s, calls, mock := newTTLTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
	})
	deleted := false
	mock.DeleteContainerFunc = func(string) error { deleted = true; return nil }

	before := time.Now().UTC()
	if err := s.stampBirthTTL(context.Background(), "alice-container", "alice", 1800); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now().UTC()

	if deleted {
		t.Error("success path must NOT delete the box")
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 SetConfig call, got %d: %+v", len(*calls), *calls)
	}
	c := (*calls)[0]
	if c.kind != "set" || c.name != "alice-container" || c.key != incus.TTLExpiresAtKey {
		t.Errorf("call = %+v, want set alice-container/%s", c, incus.TTLExpiresAtKey)
	}
