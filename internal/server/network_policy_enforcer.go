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
func (e *NetworkPolicyEnforcer) SetFlowHook(fn func(flows []netbpf.FlowRecord)) { e.flowHook = fn }

// SetExecWatcher wires the exec-telemetry watcher (#2637): Start loads its
// object after this enforcer's, every reconcile hands it the container list,
// Stop stops it. Must be called before Start.
func (e *NetworkPolicyEnforcer) SetExecWatcher(w *ExecWatcher) { e.execWatcher = w }

// SetDenyHook wires the threat-detection engine's deny-event consumer
// (#1640). Called synchronously from OnDenyEvent, alongside the existing
// audit-log write. Must be called before Start. Nil (default) leaves
// detection off.
func (e *NetworkPolicyEnforcer) SetDenyHook(fn func(ev netbpf.DenyEvent)) { e.denyHook = fn }

// TenantForIP resolves a container IP to its owning tenant id via the
// ip_tenant cache this enforcer already maintains (refreshed every 10s
// reconcile — see reconcile/applyIPTenant). Exposed for
// threatdetect.RuleContext so rules can attribute a flow endpoint without
// talking to Incus or the DB themselves. ok is false for an IP this backend
// doesn't recognize.
func (e *NetworkPolicyEnforcer) TenantForIP(ip netip.Addr) (tenantID string, ok bool) {
	if !ip.Is4() {
		return "", false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id, found := e.ipTenantInstalled[ip.As4()]
	if !found {
		return "", false
	}
	name, found := e.idName[id]
	if !found {
		return "", false
	}
	return name, true
}

// SetSignaturesEnabled opts into Tier 2 (#661) cleartext exploit-signature
// scanning on the inbound (container-receive) path: the curated built-in set is
// loaded into the BPF signature map and the per-packet scan is switched on. Must
// be called before Start. Off by default — scanning costs a per-packet bpf_loop,
// so it only runs when the operator opts in; drops still require enforce mode.
func (e *NetworkPolicyEnforcer) SetSignaturesEnabled(on bool) { e.sigEnabled = on }

// SetSignatureStore wires the operator-signature store (#661 PR-B) so the
// enforcer merges operator signatures with the built-ins. Must be called before
// Start. Nil leaves built-ins only.
func (e *NetworkPolicyEnforcer) SetSignatureStore(s NetworkPolicySignatureStore) { e.sigStore = s }

// armSignatures arms the scan gate (sig_config) and loads the initial signature
// set. Called once at Start. No-op unless opted in; logs (not fatal) when the
// loaded object predates Tier 2.
func (e *NetworkPolicyEnforcer) armSignatures() {
	if !e.sigEnabled || e.loader == nil {
		return
	}
	if !e.loader.HasSignatures() {
		log.Printf("[netpolicy] Tier 2 signatures enabled but the loaded BPF object lacks the 'signatures' map (rebuild netpolicy.bpf.o to enable #661)")
		return
	}
	n := e.refreshSignatureSlots()
	if err := e.loader.SetScanEnabled(true); err != nil {
		log.Printf("[netpolicy] enable signature scan: %v", err)
		return
	}
	log.Printf("[netpolicy] Tier 2 cleartext signature scanning ENABLED (%d signatures: built-in + operator, inbound)", n)
}

// effectiveSignatures merges the curated built-ins with the operator set (#661
// PR-B), built-ins first, capped to the BPF slot budget. A disabled operator
// signature is skipped. Returns the slice + a fingerprint so refresh can skip a
// redundant map rewrite when nothing changed.
func (e *NetworkPolicyEnforcer) effectiveSignatures() ([]netpolicy.Signature, string) {
	sigs := netpolicy.BuiltinSignatures()
	if e.sigStore != nil {
		ops, err := e.sigStore.List(e.ctx)
		if err != nil {
			log.Printf("[netpolicy] list operator signatures: %v", err)
		} else {
			for _, op := range ops {
				if !op.GetEnabled() {
					continue
				}
				if len(sigs) >= netbpf.SigMaxCount {
					log.Printf("[netpolicy] signature slot budget (%d) full; operator signature %q not loaded", netbpf.SigMaxCount, op.GetName())
					break
				}
				sigs = append(sigs, netpolicy.Signature{
					ID:      safecast.U16FromUint(op.GetId()),
					Name:    op.GetName(),
					Pattern: []byte(op.GetPattern()),
				})
			}
		}
	}
	// Fingerprint = ids+names+patterns in order (cheap; the set is tiny).
	var fp strings.Builder
	for _, s := range sigs {
		fmt.Fprintf(&fp, "%d:%s:%x;", s.ID, s.Name, s.Pattern)
	}
	return sigs, fp.String()
}

// refreshSignatureSlots writes the effective signature set into the BPF map
// (filling unused slots so a removed signature is cleared) and updates the
// id->name audit map. A no-op when the set is unchanged since the last write.
// Returns the active signature count. Safe to call every reconcile.
func (e *NetworkPolicyEnforcer) refreshSignatureSlots() int {
	if !e.sigEnabled || e.loader == nil || !e.loader.HasSignatures() {
		return 0
	}
	sigs, fp := e.effectiveSignatures()
	e.mu.Lock()
	unchanged := fp == e.sigLoaded
	e.mu.Unlock()
	if unchanged {
		return len(sigs)
	}
	names := make(map[uint16]string, len(sigs))
	for slot := 0; slot < netbpf.SigMaxCount; slot++ {
		var entry netbpf.SignatureEntry
		if slot < len(sigs) {
			entry = toSigEntry(sigs[slot])
			names[sigs[slot].ID] = sigs[slot].Name
		}
		if err := e.loader.SetSignature(safecast.U32(slot), entry); err != nil {
			log.Printf("[netpolicy] set signature slot %d: %v", slot, err)
		}
	}
	e.mu.Lock()
	e.sigNames = names
	e.sigLoaded = fp
	e.mu.Unlock()
	return len(sigs)
}

// toSigEntry converts a curated Signature into the BPF map entry, truncating an
// over-long pattern to SigMaxLen (the built-ins all fit; this is belt-and-suspenders).
func toSigEntry(s netpolicy.Signature) netbpf.SignatureEntry {
	var e netbpf.SignatureEntry
	e.ID = s.ID
	e.Enabled = true
	n := len(s.Pattern)
	if n > netbpf.SigMaxLen {
		n = netbpf.SigMaxLen
	}
	e.Len = safecast.U8(n)
	copy(e.Pattern[:], s.Pattern[:n])
	return e
}

// Start loads the BPF object, runs an initial reconcile, and launches the
// perf-ring consumer + the periodic reconcile loop (also woken by container bus
// events). Returns an error if the object fails to load (e.g. not on Linux);
// the caller logs and continues without enforcement.
func (e *NetworkPolicyEnforcer) Start(ctx context.Context) error {
	// objPath is the env value: a filesystem path, or a keyword ("embedded"/"1")
	// selecting the object compiled into the binary (#627 follow-up). Resolve
	// picks the right loader.
	loader, err := netbpf.Resolve(e.objPath)
	if err != nil {
		return err
	}
	e.loader = loader
	if !loader.SupportsEgressMode() {
		log.Printf("[netpolicy] loaded BPF object predates egress_mode (#2974): a tenant's egress_mode is applied and reported as its mode; rebuild netpolicy.bpf.o to apply it")
	}
	if !loader.SupportsInboundReplies() {
		log.Printf("[netpolicy] WARNING: loaded BPF object predates inbound-reply handling (#2618): a box with an enforce policy cannot answer connections opened toward it, and no source validation runs; rebuild netpolicy.bpf.o (or use the embedded object) before arming enforce for a tenant whose boxes serve inbound traffic")
	}
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.mu.Lock()
	e.running = true
	e.mu.Unlock()

	// Exec telemetry (#2637): the second object. Resolved from the same
	// operator value; a failure (no BTF, verifier reject, object not built) is
	// the watcher's own `unavailable (<reason>)` status and nothing else —
	// the flow program above is already loaded and keeps running.
	if e.execWatcher != nil {
		if err := e.execWatcher.Start(e.ctx, e.objPath); err != nil {
			log.Printf("[netpolicy] exec telemetry unavailable: %v (flow rules unaffected; see `containarium security sentry status`)", err)
		} else {
			log.Printf("[netpolicy] exec telemetry started (tracepoint sched/sched_process_exec)")
		}
	}

	// Tier 2 (#661): load the built-in exploit signatures + arm the scan gate once
	// (the set is static; no per-reconcile churn). No-op unless opted in.
	e.armSignatures()

	// Resolve stored policies' egress_domains once before the first reconcile
	// so the initial allow-list already includes domain IPs. The default
	// allow-list's names (#2577) are left to the refresh loop: resolving ~15
	// names here would hold up daemon startup on a host with slow or no DNS,
	// and the default is log-only, so a few seconds of would-deny rows for
	// them cost nothing.
	e.refreshDomainsForStartup()

	e.runPass(reconcileTriggerInitial)

	// Domain refresh loop (Phase C): wake when the earliest name is due —
	// min(record TTL, 60s) per name (#2379) — and re-resolve the due ones. The
	// next reconcile folds the refreshed IPs into the egress map (and diffEgress
	// prunes IPs a domain no longer resolves to).
	e.wg.Add(1)
	go e.domainRefreshLoop()

	// Perf consumer: would-deny events -> audit rows.
	rd, err := perf.NewReader(loader.EventsMap(), 4096)
	if err != nil {
		log.Printf("[netpolicy] perf reader: %v (denied-flow audit disabled)", err)
	} else {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			defer func() { _ = rd.Close() }()
			go func() { <-e.ctx.Done(); _ = rd.Close() }() // unblock Read on shutdown
			netbpf.ConsumeDenyEvents(e.ctx, rd, e, func(err error) {
				log.Printf("[netpolicy] perf: %v", err)
			})
		}()
	}

	// Flow-accounting poll (#627): read the BPF per-flow map on a cadence and
	// feed it to the traffic collector so the traffic view shows real src/dst IP
	// + byte counts sourced from eBPF. Only runs when a sink is wired AND the
	// loaded object carries the flows map (an older object still enforces; the
	// poll just stays off until the operator rebuilds it).
	switch {
	case e.flowSink == nil:
		// no sink configured — flow accounting disabled
	case !loader.HasFlowAccounting():
		log.Printf("[netpolicy] traffic-flow accounting unavailable: loaded BPF object lacks the 'flows' map (rebuild netpolicy.bpf.o to enable #627)")
	default:
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			t := time.NewTicker(e.flowPoll)
			defer t.Stop()
			e.pollFlows() // prime the view immediately rather than after one tick
			for {
				select {
				case <-e.ctx.Done():
					return
				case <-t.C:
					e.pollFlows()
				}
			}
		}()
		log.Printf("[netpolicy] traffic-flow accounting enabled (poll=%s)", e.flowPoll)
	}

	// Reconcile loop + bus trigger: the 10 s timer, plus a debounced kick on
	// container topology events (#3050).
	kick := containerTopologyKick(e.ctx, e.bus, e.eventDebounce)
	e.wg.Add(1)
	go e.reconcileLoop(kick)

	mode := "observation-only (log_only)"
	if e.enforceEnabled {
		mode = "ENFORCE ARMED — enforce-mode policies WILL DROP packets"
	}
	log.Printf("[netpolicy] enforcer started (obj=%s, interval=%s, %s)", e.objPath, e.interval, mode)
	return nil
}

// Stop cancels the loops and closes the loader (detaching every veth).
func (e *NetworkPolicyEnforcer) Stop() {
	e.mu.Lock()
	e.running = false
	// A later Start reports "not reconciled" until its own first pass.
	e.applied = nil
	e.appliedAt = time.Time{}
	e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	if e.execWatcher != nil {
		e.execWatcher.Stop()
	}
	if e.loader != nil {
		if err := e.loader.Close(); err != nil {
			log.Printf("[netpolicy] loader close: %v", err)
		}
	}
}

// OnDenyEvent implements netbpf.DenyEventSink: write a would-deny flow to the
// audit log. Action network_policy.deny; the tenant name is resolved from the
// id->name map built during reconcile.
func (e *NetworkPolicyEnforcer) OnDenyEvent(ctx context.Context, ev netbpf.DenyEvent) {
	// #1640: hand every deny event to the threat-detection engine, same as
	// the audit write below — nil hook is the default (sentry disabled).
	e.mu.Lock()
	tenant := e.idName[ev.TenantID]
	e.mu.Unlock()
	dropped := e.denyEventDropped(ev)
	// The hook learns whether the packet was really dropped (#2612): the BPF
	// program emits the event for a log_only would-deny exactly as for a drop.
	if e.denyHook != nil {
