package server

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ebpf/perf"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/audit"
	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/traffic"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// containerSuffix is the trailing tag every tenant container name carries; the
// tenant name is the part before it (mirrors container_server.go).
const containerSuffix = "-container"

// defaultNetPolicyReconcileInterval is how often the enforcer re-converges the BPF maps
// to the live container + policy state. The reconcile loop is the source of
// truth (robust against dropped bus events); the bus only triggers an early
// reconcile for latency. Each cycle does one Incus ListContainers plus a cheap
// per-container netlink lookup; the expensive per-container Incus inspect is
// cached and only re-runs when a container starts/restarts (#654).
const defaultNetPolicyReconcileInterval = 10 * time.Second

// defaultFlowPollInterval is how often the enforcer reads the BPF per-flow
// accounting map (#627) and feeds it to the traffic collector. Shorter than the
// reconcile interval isn't useful (the counters are cumulative); a few seconds
// keeps the traffic view fresh without churning the map iterator.
const defaultFlowPollInterval = 15 * time.Second

// defaultFlowIdleTimeout is how long a flow may go without a packet before the
// poll loop treats it as closed: persists its final counters to history (#632)
// and deletes it from the BPF map. The BPF program never deletes a flow on
// FIN/RST, and the 65536-entry LRU map only evicts under pressure — so without
// this sweep a quiesced flow on a lightly-loaded backend never reaches history
// (the gap on-backend validation surfaced). Two minutes is long enough that a
// briefly-idle but still-live connection isn't split, short enough that an ended
// flow lands in history promptly.
const defaultFlowIdleTimeout = 2 * time.Minute

// FlowSink receives per-flow accounting records sourced from the eBPF program
// (#627). *traffic.Collector implements it; kept an interface so the enforcer is
// testable without the collector.
type FlowSink interface {
	IngestEBPFFlows(flows []traffic.EBPFFlow)
	// PersistEBPFFlows writes a batch of closed/idle flows straight to history,
	// independent of IngestEBPFFlows' disappearance diff — the idle reaper (#632).
	PersistEBPFFlows(flows []traffic.EBPFFlow)
}

// containerInspector is the slice of the Incus client the enforcer needs.
type containerInspector interface {
	ListContainers() ([]incus.ContainerInfo, error)
	GetRawInstance(name string) (map[string]string, string, error)
	// GetRawInstanceDevices is the instance's expanded device map, the
	// host-side source of its NIC's network and static address (#2618).
	GetRawInstanceDevices(name string) (map[string]map[string]string, error)
}

// NetworkPolicyEnforcer owns the netbpf.Loader and keeps the kernel's
// per-tenant network-policy maps converged with the daemon's stored policies +
// live containers (#315 Phase A piece 6c). It is OFF by default — NewDualServer
// only constructs it when a BPF object path is configured — so an existing
// deployment is unaffected unless the operator opts in.
//
// Phase A is observation-only: the program never drops, it emits would-deny
// events that this enforcer's perf consumer turns into audit rows.
type NetworkPolicyEnforcer struct {
	objPath        string // BPF object source: a path, or a keyword ("embedded"/"1") → netbpf.Resolve
	store          NetworkPolicyStore
	registry       TenantRegistry
	insp           containerInspector
	audit          *audit.Store
	bus            *events.Bus
	interval       time.Duration
	enforceEnabled bool // daemon-wide guard: ENFORCE policies only drop when true

	// eventDebounce is the trailing debounce on the container-event path
	// (#3050); the timer path (interval) is not debounced.
	eventDebounce time.Duration
	// meter counts reconcile passes (#3050). pass is the reconcile a pass runs;
	// nil means e.reconcile (a test substitutes a fake).
	meter *reconcileMeter
	pass  func(context.Context) error

	// gateway is the bridge gateway, the box's DNS resolver. A policy with an
	// egress preset (#2440) implicitly allows it; the zero value means the
	// daemon could not name it and the preset adds nothing.
	gateway netip.Addr

	// defaultExtras is the operator's addition to the default egress allow-list;
	// defaultOn is whether a tenant with no stored policy gets that list
	// (#2577 stage 1). Set before Start.
	defaultOn      bool
	defaultExtras  netpolicy.DefaultEgressExtras
	defaultDomains []string // normalized built-ins + extras: what the resolver caches and the policy looks up

	resolver *DomainResolver // Phase C: egress_domains -> IPs (refresh cadence: min(TTL, 60s), #2379)

	flowSink        FlowSink      // #627: traffic-view flow accounting (nil = disabled)
	flowPoll        time.Duration // how often to read the BPF flows map
	flowIdleTimeout time.Duration // #632: idle age past which a flow is reaped to history

	// flowHook and denyHook feed the threat-detection engine (#1640): the
	// same per-poll flow batch and per-event deny stream this enforcer
	// already collects, fanned out to a second consumer. nil = sentry
	// disabled. Same one-callback pattern as Scanner.SetScanResultHook /
	// auto_quarantine.go — the one existing detect→act template.
	flowHook func(flows []netbpf.FlowRecord)
	denyHook func(ev netbpf.DenyEvent)

	loader *netbpf.Loader

	// execWatcher is the exec-telemetry userspace half (#2637). It rides this
	// enforcer's lifecycle and 10 s reconcile (the container list with init
	// pids is already gathered here) but loads its own BPF object, so its
	// failure never touches flow enforcement. nil = sentry off.
	execWatcher *ExecWatcher
	// lastContainers is the raw ListContainers result of the latest gather,
	// handed to the exec watcher's reconcile. Only touched from reconcile.
	lastContainers []incus.ContainerInfo

	// vethCache maps a running container name -> its host veth name and primary
	// NIC facts, so a steady reconcile resolves the ifindex with a cheap local
	// netlink lookup (VethIndex) instead of an Incus inspect (GetRawInstance +
	// GetRawInstanceDevices) every cycle (#654). Only touched from gather, which
	// runs serially inside reconcile (the reconcile loop is single-goroutine), so
	// it needs no lock.
	vethCache map[string]vethInfo

	// leaseDir is where the bridge DHCP lease files live (#2618); derived from
	// the Incus data dir (SetIncusDataDir), tests point it at a fixture.
	leaseDir string
	// addrWarned holds the running containers already warned that the host
	// knows no address for them (#2618 S5); cleared when known or stopped.
	addrWarned map[string]bool
	// extraUplinks are operator-named uplink interfaces
	// (CONTAINARIUM_NETWORK_POLICY_UPLINKS) added to the default-route ones.
	extraUplinks []string
	// trustedInstalled is what the kernel trusted_iif map holds (ifindex set).
	trustedInstalled map[int]bool
	// uplinks dedupes the refusal warning and the trusted-set log line (#3185).
	uplinks uplinkReconciler
	// reseedLast is the inbound_reseed counter at the previous pass.
	reseedLast uint64

	// egressModeWarned holds the tenant ids already warned that the loaded
	// object ignores their egress_mode (#2987), so the warning is not repeated
	// every pass. Only touched from reconcile.
	egressModeWarned map[uint32]bool

	// egressNames maps a tenant id to its name for every tenant seen this pass
	// or still holding allow-list entries, so a failed write is attributed to
	// its tenant even when the tenant has no box left (#3051). Only touched from
	// reconcile.
	egressNames map[uint32]string

	sigEnabled bool                        // #661: populate + scan Tier 2 signatures (operator opt-in)
	sigStore   NetworkPolicySignatureStore // #661 PR-B: operator signatures (nil = built-ins only)
	sigNames   map[uint16]string           // signature id -> name, for audit labelling (built at populate)
	sigLoaded  string                      // last applied signature set fingerprint (skip redundant map writes)

	mu                sync.Mutex
	attached          map[int]string                      // ifindex -> container name currently attached
	idName            map[uint32]string                   // tenant id -> name (for audit/log)
	enforced          map[uint32]bool                     // tenant ids whose effective mode is ENFORCE (deny == dropped)
	egressInstalled   map[netbpf.EgressEntry]bool         // egress LPM entries currently in the map
	denyInstalled     map[netbpf.DenyKey]netbpf.DenyEntry // virtual-patch deny entries currently in the map (#660)
	ipTenantInstalled map[[4]byte]uint32                  // ip_tenant entries currently in the map (#923: converge deletes)
	running           bool                                // the BPF program is loaded (Start succeeded, Stop not yet called)
	applied           map[string]tenantApplied            // per-tenant state the last reconcile pass loaded; nil until a pass completes
	appliedAt         time.Time                           // when the pass that produced applied ended

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewNetworkPolicyEnforcer(objPath string, store NetworkPolicyStore, registry TenantRegistry, insp containerInspector, auditStore *audit.Store, bus *events.Bus, enforceEnabled bool) *NetworkPolicyEnforcer {
	return &NetworkPolicyEnforcer{
		objPath:           objPath,
		store:             store,
		registry:          registry,
		insp:              insp,
		audit:             auditStore,
		bus:               bus,
		interval:          defaultNetPolicyReconcileInterval,
		eventDebounce:     defaultContainerEventDebounce,
		meter:             globalReconcileMeter(),
		enforceEnabled:    enforceEnabled,
		resolver:          NewDomainResolver(nil),
		flowPoll:          defaultFlowPollInterval,
		flowIdleTimeout:   defaultFlowIdleTimeout,
		vethCache:         make(map[string]vethInfo),
		leaseDir:          defaultIncusLeaseDir,
		addrWarned:        make(map[string]bool),
		extraUplinks:      splitCSV(os.Getenv("CONTAINARIUM_NETWORK_POLICY_UPLINKS")),
		trustedInstalled:  make(map[int]bool),
		egressNames:       make(map[uint32]string),
		attached:          make(map[int]string),
		idName:            make(map[uint32]string),
		enforced:          make(map[uint32]bool),
		egressInstalled:   make(map[netbpf.EgressEntry]bool),
		denyInstalled:     make(map[netbpf.DenyKey]netbpf.DenyEntry),
		ipTenantInstalled: make(map[[4]byte]uint32),
	}
}

// SetDefaultEgress turns the default egress posture on for tenants with no
// stored policy (#2577 stage 1): the built-in allow-list plus extras, always in
// log_only. extras that do not validate are refused so a typo cannot quietly
// narrow the list; the caller decides what to run with instead. Must be called
// before Start.
func (e *NetworkPolicyEnforcer) SetDefaultEgress(extras netpolicy.DefaultEgressExtras) error {
	c, err := netpolicy.DefaultPolicy("probe", extras)
	if err != nil {
		return err
	}
	e.defaultOn, e.defaultExtras, e.defaultDomains = true, extras, c.EgressDomains
	return nil
}

// defaultEgressState is how the default egress posture came up at startup.
type defaultEgressState int

const (
	defaultEgressOff          defaultEgressState = iota // CONTAINARIUM_NETWORK_DEFAULT_EGRESS=off
	defaultEgressFull                                   // built-in list plus the operator's extras
	defaultEgressBuiltinsOnly                           // extras refused; built-in list only
)

// configureDefaultEgress applies the CONTAINARIUM_NETWORK_DEFAULT_EGRESS*
// settings to the enforcer and logs the outcome. Extras that do not validate
// are refused with the reason and the built-in list runs alone, so one typo
// cannot disable the posture or narrow it silently.
func configureDefaultEgress(e *NetworkPolicyEnforcer, mode, domains, cidrs string) defaultEgressState {
	on, extras := netpolicy.ParseDefaultEgress(mode, domains, cidrs)
	if !on {
		log.Printf("NetworkPolicy: default egress posture is off (%s=off); tenants with no stored policy run an empty allow-list in log_only", appconfig.EnvDefaultEgress)
		return defaultEgressOff
	}
	if err := e.SetDefaultEgress(extras); err != nil {
		log.Printf("NetworkPolicy: ignoring %s/%s (%v); using the built-in default allow-list only", appconfig.EnvDefaultEgressDomains, appconfig.EnvDefaultEgressCIDRs, err)
		if err := e.SetDefaultEgress(netpolicy.DefaultEgressExtras{}); err != nil {
			// Unreachable while the built-ins are valid; say so rather than
			// claim a list that is not applied.
			log.Printf("NetworkPolicy: the built-in default egress allow-list did not load (%v); the default posture is off", err)
			return defaultEgressOff
		}
		log.Printf("NetworkPolicy: built-in default egress allow-list (log_only, no extras) applies to tenants with no stored policy")
		return defaultEgressBuiltinsOnly
	}
	log.Printf("NetworkPolicy: default egress allow-list (log_only) applies to tenants with no stored policy")
	return defaultEgressFull
}

// defaultPolicyFor builds the default policy for a tenant, resolved the same
// way as a stored one. The second result is false when the posture is off.
func (e *NetworkPolicyEnforcer) defaultPolicyFor(tenant string) (netpolicy.CompiledPolicy, bool) {
	if !e.defaultOn {
		return netpolicy.CompiledPolicy{}, false
	}
	c, err := netpolicy.DefaultPolicy(tenant, e.defaultExtras)
	if err != nil {
		log.Printf("[netpolicy] default policy for %q: %v", tenant, err)
		return netpolicy.CompiledPolicy{}, false
	}
	return e.expandPolicy(c, false), true
}

// SetImplicitGateway tells the enforcer the bridge gateway address, the
// DNS resolver every box uses, so a policy with an egress preset (#2440) can
// allow it without the operator listing it. Must be called before Start.
func (e *NetworkPolicyEnforcer) SetImplicitGateway(a netip.Addr) { e.gateway = a }

// SetFlowSink wires the traffic collector so the enforcer can feed it per-flow
// accounting read from the BPF flows map (#627). Must be called before Start.
// Nil leaves flow accounting off.
func (e *NetworkPolicyEnforcer) SetFlowSink(s FlowSink) { e.flowSink = s }

// SetFlowHook wires the threat-detection engine's flow consumer (#1640).
// Called synchronously at the end of every flow-accounting poll (15s
// cadence) with that poll's active flow batch — the same records
// flowsToEBPF turns into traffic-view rows. Must be called before Start.
// Nil (default) leaves detection off.
