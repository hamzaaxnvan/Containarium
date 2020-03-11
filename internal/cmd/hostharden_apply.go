//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/hostharden"
)

// applyMetadataBlock is the #1103 fix-3 mitigation shared by `cloud enroll`
// and `pool join`: block the container bridge's FORWARDED traffic to the
// cloud metadata endpoint, then install a systemd unit so the rule survives
// a reboot.
//
// Deliberately has no opt-out. This is the control that stops a tenant
// workload from pivoting through the bridge to the host's cloud identity
// (GCP/AWS/Azure instance credentials) — a flag letting an operator
// consciously disable it would hand that same escape hatch to whoever runs
// `cloud enroll`/`pool join` on a shared or misconfigured host, which is
// exactly the class of breach #1103 exists to close. A GENUINE failure
// (missing iptables, bridge not yet created, nftables-only host) is still
// only a printed warning, never a returned error — that's an environment
// gap, not a policy choice, and must not turn a successful join/enroll
// into a reported failure. The two are not the same thing: one is "we
// couldn't", the other would have been "you told us not to".
//
// No !windows tag: this file has the same build constraint as cloud.go (the
// only caller that also builds on Windows), and on Windows the resulting
// exec.Command calls simply fail fast (no iptables/incus binaries) and get
// reported the same non-fatal way.
