package server

import (
	"errors"
	"testing"
	"time"
)

// newPendingServer builds the minimal ContainerServer needed to exercise the
// pending-creation bookkeeping (#1035) — no manager, no backend.
func newPendingServer(entries ...*PendingCreation) *ContainerServer {
	s := &ContainerServer{pendingCreations: map[string]*PendingCreation{}}
	for _, e := range entries {
		s.pendingCreations[e.Username] = e
	}
	return s
}

// TestCancelPendingCreation pins the state machine a delete-during-create
// relies on: only a genuinely in-flight creation can be claimed, and claiming
// it is idempotent so a retried delete doesn't double-report.
func TestCancelPendingCreation(t *testing.T) {
	cases := []struct {
		name    string
		pending *PendingCreation
		want    bool
	}{
