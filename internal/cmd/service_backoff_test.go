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
