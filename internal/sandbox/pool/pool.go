// Package pool implements the warm-pool reconciler and claim ring for
// ephemeral sandboxes (#1488 Phase 3): "Pool members are created and
// started by a background reconciler. A claim touches an instance that
// has been running for seconds to minutes" instead of paying boot cost on
// the request path.
//
// A pool member is a whole Incus container, dedicated to exactly one
// task, destroyed on release and never reused — see Release's doc
// comment and the design note's Isolation section for why. Ready members
// are tracked per SandboxTemplate ("shared" means shared across tenants,
// not across templates): v1 configures the base template only.
//
// This package talks to the same incus.Backend-shaped interface
// SandboxServer (internal/server/sandbox_server.go) already uses to
// create/destroy sandboxes directly — not pkg/core/container.Manager or
// box.BoxBackend, both of which are built around the full persistent-box
// identity flow (SSH keys, tenant naming) that pool members, like cold-
// path sandboxes, explicitly skip. A pool member and a cold-path sandbox
// should be indistinguishable in shape once claimed.
//
// Pure Go control-plane state plus calls to a narrow, fakeable Backend
// interface — no live Incus host needed to build or test this package,
// per the design note's own scoping ("pool logic tests run in
// milliseconds with no host"). The nightly latency test against a real
// host is Phase 3's actual exit criterion and lives elsewhere.
package pool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/sandbox/ipam"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Backend is the subset of incus.Backend the pool reconciler needs to
// warm and destroy members. Any concrete type satisfying incus.Backend
// (in particular *incus.Client, the real thing) automatically satisfies
// this narrower interface too — no adapter required — while a test fake
// only has to implement four methods instead of incus.Backend's full
// surface.
type Backend interface {
	CreateContainer(config incus.ContainerConfig) error
	StartContainer(name string) error
	StopContainer(name string, force bool) error
	DeleteContainer(name string) error
}

// ErrPoolExhausted is returned by Claim when no ready member exists for
// the requested template.
var ErrPoolExhausted = errors.New("pool: no ready member for this template")

// Member is the pool's typed view of one pool member — a named struct,
// not the map[string]string the design note's Contracts section
// specifically calls out as the wrong shape for this.
type Member struct {
	ID       string
	Template pb.SandboxTemplate
	IP       string
	WarmedAt time.Time
}

// Config controls what Reconcile keeps warm.
type Config struct {
	// MinWarm is the target ready-member count, per template. A template
	// with no entry (or 0) is never warmed — v1 configures BASE only.
