//go:build !windows && !containarium_client

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Restart-backoff guards for the three unit templates (#1152).
//
// The incident: a production backend crash-looped 1655 times in ~4 hours —
// one restart every ~7 seconds — until an operator noticed the host's CPU
// was pinned. `StartLimitIntervalSec=0` disables systemd's rate limiting
// entirely, and with a flat `RestartSec=5s` a persistent start failure
// retries forever at full speed.
//
// The collateral damage was worse than the CPU: each cycle rewrote the
// bundled monitoring stack's config, so alertmanager and vmalert were
// restarted every 7 seconds for 4 hours. Alerting could not have fired
// during the incident — including on the incident itself.
//
// The fix keeps infinite retry (that part was deliberate: a pool member
// that stays dead is worse than one that retries) and adds geometric
// backoff, so the two properties stop being conflated.

// unitSources returns each unit template with a label. The daemon
// template is a package const; the sibling shipped as a plain file and
// the sentinel's inline template are read from source, because nothing
// else in the build would notice them regressing.
func unitSources(t *testing.T) map[string]string {
	t.Helper()

	read := func(parts ...string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatalf("read %v: %v", parts, err)
		}
		return string(raw)
	}

	// The sentinel entry is the GENERATED unit, not its source text. It used
	// to be read("sentinel_service.go"); when #1350 moved the template into
	// sentinel_unit.go, that read kept succeeding against a file that no
	// longer held a unit, and every guard below silently stopped covering the
	// sentinel. Rendering it makes these tests immune to where the template
	// lives — and is closer to what actually ships anyway.
	sentinelUnit, err := buildSentinelUnit(sentinelUnitConfig{
		SpotVM:     "spot-vm",
