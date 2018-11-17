package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/transfer"
)

// TestSentinelHint_RewritesUnresolvedError verifies that an unresolved-sentinel
// failure from transfer is turned into agent-native guidance: point at
// get_container / the connect tool, and explicitly say the env var is NOT
// required. This is the fix for agents that chased CONTAINARIUM_SENTINEL_HOST
// and gave up (issue #658).
func TestSentinelHint_RewritesUnresolvedError(t *testing.T) {
	err := sentinelHint("push", "alice", fmt.Errorf("%w: boom", transfer.ErrSentinelUnresolved))
	msg := err.Error()

	for _, want := range []string{"get_container", "ssh_host", "connect", "alice"} {
		if !strings.Contains(msg, want) {
			t.Errorf("guidance missing %q; got: %s", want, msg)
