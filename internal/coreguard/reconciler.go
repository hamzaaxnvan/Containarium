package coreguard

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// Mode, ParseMode and ErrUnsupportedFirewall are shared with the tenant
// guard (internal/nicguard). Unset arms the guard; only "off" disables.
type Mode = nicguard.Mode

const (
	ModeOff     = nicguard.ModeOff
	ModeEnforce = nicguard.ModeEnforce
)

// ParseMode maps the CONTAINARIUM_CORE_GUARD value to a Mode.
func ParseMode(s string) Mode { return nicguard.ParseMode(s) }

// ErrUnsupportedFirewall is returned when the host's Incus firewall driver
// cannot enforce bridge NIC ACLs (only nftables can).
var ErrUnsupportedFirewall = nicguard.ErrUnsupportedFirewall

// DefaultInterval is the steady-state reconcile cadence; bus events
// converge sooner.
const DefaultInterval = 60 * time.Second

// Config is the reconciler's static wiring.
type Config struct {
	Mode Mode
	// Bridge is the Incus network the core containers' NIC sits on
	// (e.g. incusbr0) — what EnsureNICDevice binds the instance-local
	// override to.
	Bridge string
	// BridgeCIDR is the bridge's ipv4.address as Incus reports it, gateway
	// form ("10.100.0.1/24"). Network form ("10.100.0.0/24") is accepted;
	// the gateway is then base+1, Incus's convention.
	BridgeCIDR string
	// NICDevice is the device name to guard on each core container
	// (default eth0).
	NICDevice string
	// Interval overrides DefaultInterval (tests).
	Interval time.Duration
}

// Entry is one core container's guard state as of the last pass.
type Entry struct {
	Container    string
	Role         incus.Role
	IP           string
	ACLName      string
	Attached     bool
	RulesApplied int
	LastError    string
}

// Status is the reconciler's last-pass snapshot, read by the status RPC.
type Status struct {
	Mode           Mode
	FirewallDriver string
	Entries        []Entry
	UnknownRoles   []incus.Role
	// StaleSince is set when the last pass could not read the host; the
	// ACLs from the previous good pass stay in force meanwhile.
	StaleSince time.Time
	LastError  string
	LastPass   time.Time
}

// Reconciler keeps the core containers' NIC ACLs equal to what Compute says
// they should be. It only ever writes on drift.
type Reconciler struct {
	be  incus.Backend
	cfg Config

	mu     sync.Mutex
	status Status
}

// NewReconciler wires a reconciler; nothing runs until ReconcileOnce or
