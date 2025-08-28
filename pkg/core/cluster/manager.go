package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VMHost is the narrow host surface the manager drives — implemented
// by IncusHost in production and by a fake in tests, so orchestration
// sequences are unit-testable (the nodevm Runner-seam precedent).
type VMHost interface {
	// VMCapable returns nil when the host can run VMs, or a typed,
	// user-facing error when it cannot (the design's hard create-time
	// requirement).
	VMCapable() error
	// ContainerNodeCapable is the container-class counterpart
	// (#1429): nil when the host can run k3s node containers, or a
	// refusal naming every missing (or unverifiable) precondition.
	ContainerNodeCapable() error
	// NodeCapacityCapable is the per-node half of the container probe
	// (#1439): nil when a container node of this size can advertise a
	// truthful capacity on this host, a refusal when it cannot.
	NodeCapacityCapable(spec NodeSpec) error
	// CreateNode creates one cluster node backed by the instance type
	// the isolation class selects: an Incus VM, or a system container
	// carrying the container node profile (#1429).
	CreateNode(spec NodeSpec, isolation Isolation) error
	Start(name string) error
	// Stop halts a node. Deleting a running instance is refused by
	// Incus, so this is a required step of removal, not a nicety.
	Stop(name string) error
	Delete(name string) error
	// WaitReady blocks until the guest agent is up and networked,
	// returning the VM's primary IP.
	WaitReady(name string, timeout time.Duration) (string, error)
	Push(name, path string, content []byte, mode string) error
	Read(name, path string) ([]byte, error)
	Exec(name string, cmd []string) (string, error)
	// ClusterVMs lists this cluster's VMs by label, bucketed as the
	// reconciler's Observed shape.
	ClusterVMs(tenant, clusterName string) (Observed, error)
}

// Manager provisions cluster VMs. It is stateless: every method takes
// what it needs, and persistence stays with the caller.
type Manager struct {
	host VMHost
	// k3sBinary loads the host-cached, checksum-verified binary
	// (EnsureK3s). A loader keeps ~70MB out of memory until a
	// provision actually happens.
	k3sBinary func() ([]byte, error)
	// waitReadyTimeout is how long a fresh VM gets to boot + network.
	waitReadyTimeout time.Duration
	// deleteRetryDelay is the pause between DeleteVM's retried Delete
	// attempts (#1882). A test field, like waitReadyTimeout: production
	// uses deleteRetryDelayDefault, tests shrink it so the retry loop
	// doesn't slow the suite down.
	deleteRetryDelay time.Duration
}

// NewManager builds a Manager on a host. artifactBase is the host
// cache root (DefaultArtifactBase in production).
func NewManager(host VMHost, artifactBase string) *Manager {
	return &Manager{
		host: host,
		k3sBinary: func() ([]byte, error) {
			path, err := EnsureK3s(context.Background(), artifactBase)
			if err != nil {
				return nil, err
			}
			return os.ReadFile(path)
		},
		waitReadyTimeout: 3 * time.Minute,
		deleteRetryDelay: deleteRetryDelayDefault,
	}
}

// NewManagerWithLoader builds a Manager with an explicit binary
// loader — the seam tests (and callers with pre-staged artifacts) use
// instead of the EnsureK3s download path.
func NewManagerWithLoader(host VMHost, loader func() ([]byte, error)) *Manager {
	return &Manager{host: host, k3sBinary: loader, waitReadyTimeout: 3 * time.Minute, deleteRetryDelay: deleteRetryDelayDefault}
}

// VMCapable surfaces the host's VM capability probe.
func (m *Manager) VMCapable() error { return m.host.VMCapable() }

// ContainerNodeCapable surfaces the host's container-node probe (#1429).
func (m *Manager) ContainerNodeCapable() error { return m.host.ContainerNodeCapable() }

// Observe returns the host's view of a cluster's VMs.
func (m *Manager) Observe(tenant, clusterName string) (Observed, error) {
	return m.host.ClusterVMs(tenant, clusterName)
}

// StartVM restarts a stopped cluster VM.
func (m *Manager) StartVM(name string) error { return m.host.Start(name) }

// deleteRetries bounds DeleteVM's retried Delete attempts (#1882): a
// genuinely broken instance must still surface as a real, reportable
// failure, not retry forever inside one RPC.
const deleteRetries = 3

// deleteRetryDelayDefault is production's pause between retried Delete
// attempts.
const deleteRetryDelayDefault = 2 * time.Second

// DeleteVM removes a cluster node, stopping it first.
//
// Incus refuses to delete a running instance ("Instance is running"),
// so the stop is part of the removal rather than an optimisation. The
// tenant container path has always done this; the cluster path did
// not, which meant the autoscaler could never complete a scale-down
// and `cluster delete` left every instance of the cluster running on
// the host (#1475).
//
// A stop error is deliberately ignored: the common case is a node that
// is already stopped, which Incus reports as an error, and any stop
// failure that actually matters resurfaces as a delete failure.
//
// The delete itself is retried a bounded few times (#1882): Stop's own
// operation completing does not guarantee the instance's queryable
// state has settled to Stopped by the time the very next API call
// runs, and Incus can answer that brief window with the same "Instance
// is running" error a genuinely-still-running instance would — one
// autoscaler run hung an entire 30-minute scale-down budget on exactly
// this for one node in a batch, while a sibling node in the same batch
// deleted cleanly on its first attempt. The delete's error is still
// what the caller ultimately sees after retries are exhausted — the
// autoscaler retries on it too, so swallowing it would make a failed
// scale-down look successful.
func (m *Manager) DeleteVM(name string) error {
	_ = m.host.Stop(name)
	var err error
	for attempt := 0; attempt < deleteRetries; attempt++ {
		if err = m.host.Delete(name); err == nil {
			return nil
		}
		if attempt < deleteRetries-1 {
			time.Sleep(m.deleteRetryDelay)
		}
	}
	// #1882: this path previously had no logging at all, which is
	// exactly what made a 30-minute scale-down hang undiagnosable after
	// the fact — the daemon log had zero trace of the delete ever being
	// attempted. Logged only here, once retries are truly exhausted, so
	// a transient race that resolved on retry stays silent.
	log.Printf("[cluster] delete %s: still failing after %d attempt(s): %v", name, deleteRetries, err)
	return err
}

// abandon removes an instance whose provisioning failed after it was
// created, and returns the original error.
//
// Decide() re-emits ActionCreateCP / ActionCreateWorker only when an
// instance is MISSING, so anything left behind here is never retried
// and never replaced: the cluster stays in error with a running
// instance consuming its full size, and on the worker path the store
// never learns about it either — invisible to `cluster status` and to
// the autoscaler, exactly the shape of #1498's leaked node.
//
// The removal is best-effort by design: the provisioning failure is
// what the caller needs to see, and a delete that also fails only adds
// a second symptom of the same broken host. It is logged so the orphan
// is not silent.
func (m *Manager) abandon(name string, cause error) error {
	if err := m.DeleteVM(name); err != nil {
		log.Printf("[cluster] %s failed to provision (%v) and could not be removed (%v); "+
			"it will not be retried while the instance exists", name, cause, err)
