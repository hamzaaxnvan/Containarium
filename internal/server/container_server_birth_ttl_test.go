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
	stamped, err := time.Parse(time.RFC3339, c.value)
	if err != nil {
		t.Fatalf("stamped value %q not RFC3339: %v", c.value, err)
	}
	lo := before.Add(30 * time.Minute).Add(-2 * time.Second)
	hi := after.Add(30 * time.Minute).Add(2 * time.Second)
	if stamped.Before(lo) || stamped.After(hi) {
		t.Errorf("stamped expiry %s outside [%s, %s]", stamped, lo, hi)
	}
}

// TestStampBirthTTL_FailureDeletesBox — if the TTL can't be stamped, the box
// is deleted and an Internal error is returned: a box that asked to be
// ephemeral but would leak forever is worse than no box (default-dead, #522).
func TestStampBirthTTL_FailureDeletesBox(t *testing.T) {
	s, _, mock := newTTLTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
	})
	mock.SetConfigFunc = func(string, string, string) error {
		return errors.New("incus config write failed")
	}
	deletedName := ""
	mock.DeleteContainerFunc = func(name string) error { deletedName = name; return nil }

	err := s.stampBirthTTL(context.Background(), "alice-container", "alice", 1800)
	if err == nil {
		t.Fatal("expected an error when the TTL stamp fails")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Internal {
		t.Errorf("error = %v, want Internal status", err)
	}
	if deletedName != "alice-container" {
		t.Errorf("box not deleted on stamp failure: deletedName=%q want alice-container", deletedName)
	}
}

// TestStampBirthAutoSleep_EnablesWithThreshold — #524: a create-time idle-stop
// enables auto-sleep at birth by writing the same two Incus keys ToggleAutoSleep
// uses, so the box is born with its idle→stop timer (no separate toggle call).
func TestStampBirthAutoSleep_EnablesWithThreshold(t *testing.T) {
	s, calls, _ := newTTLTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
	})
	s.stampBirthAutoSleep("alice-container", 20)

	if len(*calls) != 2 {
