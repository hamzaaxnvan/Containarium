// Command netpolicy-smoke is a throwaway end-to-end smoke test for the eBPF
// network-policy *enforcer* (#315 Phase A piece 6c). Where cmd/ebpf-phaseA
// exercises the bare loader, this drives the real server.NetworkPolicyEnforcer:
// it constructs the enforcer with an in-memory policy store + tenant registry +
// a real Incus client, seeds a policy for a test tenant, then Start()s it so the
// enforcer's own reconcile loop discovers the live container, resolves its veth,
// populates the BPF maps, attaches the program, and runs the perf consumer.
//
// Run it on a backend, create a `<tenant>-container`, generate traffic, and
// watch the enforcer log "would-deny" lines for flows outside the seeded
// allow-list (and stay silent for flows inside it). Observation-only — nothing
// is dropped.
//
// THROWAWAY. Not built into releases. Run with --help.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/server"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type cidrList []string
