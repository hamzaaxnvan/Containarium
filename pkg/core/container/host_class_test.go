package container

import (
	"sync"
	"testing"
)

// TestHostClassDetection covers the gating contract for the
// jump-server's GCP-specific code paths (issue #351). The actual
// useradd/systemctl calls in retryUseraddWithLockWait need root and
// a real host, so we test what we CAN cheaply test: that isGCPHost
// returns the detector's verdict and that the detector is replaceable
// for callers that want deterministic behavior in tests.
func TestHostClassDetection(t *testing.T) {
	t.Run("isGCPHost returns true when detector reports GCP", func(t *testing.T) {
