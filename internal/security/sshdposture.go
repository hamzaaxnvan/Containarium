package security

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// DefaultSSHDPostureInterval is how often every running box's sshd
// configuration is re-read and the managed drop-in re-asserted. Minutes,
// not the ClamAV scanner's day: a tenant edit must not stay live for long,
// and the pass is a handful of file reads per box through the incus file
// API — no rootfs mount, no exec inside the box.
const DefaultSSHDPostureInterval = 10 * time.Minute

// sshdReloadScript reloads whichever unit name the box's distro uses
// (Debian: ssh, RHEL: sshd). A box whose sshd is not running has nothing
// to reload and that is not an error here.
const sshdReloadScript = "systemctl reload sshd 2>/dev/null || systemctl reload ssh 2>/dev/null || true"

// SSHDPostureReconciler keeps every running box's sshd key-only (#2424).
//
// Anything written inside a box is advisory — the owner is root there and
// can delete the drop-in, add one that sorts earlier, or run a second sshd.
// The guarantee lives outside the box (the sentinel offers no password
// method and pipes to the key-only host sshd; the tenant guard limits who
// can reach a box's port 22). This reconciler is the layer that keeps the
// stock sshd honest and makes tampering VISIBLE: each pass re-reads the
// effective configuration through the incus file API, rewrites the managed
// drop-in if it is missing or altered, and records what it found as a
// security finding so a tenant edit lasts at most one interval and leaves
// an audit record.
//
// Backfill is silent: a box that has never been brought under policy
// (no marker in its incus config — host-side state the tenant cannot
// touch) gets the drop-in without a finding, because the image default is
// not the tenant's doing. From then on, a missing or altered drop-in is.
type SSHDPostureReconciler struct {
	incus    incus.Backend
	sink     threatdetect.FindingSink // nil = log only
	interval time.Duration
	now      func() time.Time

	mu        sync.Mutex
	backendID string
	cancel    context.CancelFunc

	// listenerCheck enables the rogue SSH listener probe (#2439), one exec
	// per running box per pass. On unless an operator turns it off.
	listenerCheck bool
}

// NewSSHDPostureReconciler builds a reconciler over backend. sink may be
// nil (findings are then only logged); interval <= 0 means
// DefaultSSHDPostureInterval.
func NewSSHDPostureReconciler(backend incus.Backend, sink threatdetect.FindingSink, interval time.Duration) *SSHDPostureReconciler {
	if interval <= 0 {
		interval = DefaultSSHDPostureInterval
	}
	return &SSHDPostureReconciler{incus: backend, sink: sink, interval: interval, now: time.Now, listenerCheck: true}
}

// SetListenerCheck turns the rogue SSH listener probe (#2439) on or off.
// It is on by default; it costs one exec per running box per pass.
func (r *SSHDPostureReconciler) SetListenerCheck(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listenerCheck = on
}

func (r *SSHDPostureReconciler) listenerCheckEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listenerCheck
}

// SetBackendID sets the backend id stamped onto findings. Deferred to the
// daemon's Start, like the threat engine's, because the local backend id
// is not known until the peer pool starts.
func (r *SSHDPostureReconciler) SetBackendID(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backendID = id
}

func (r *SSHDPostureReconciler) getBackendID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.backendID
}

// Start runs a pass shortly after startup, then every interval, until ctx
// is cancelled or Stop is called.
func (r *SSHDPostureReconciler) Start(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(ctx)
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s := r.ReconcileAll(ctx)
				log.Printf("[sshd-posture] pass: %s", s)
				timer.Reset(r.interval)
			}
		}
	}()
	log.Printf("Box sshd posture reconciler started (interval: %v)", r.interval)
}

// Stop ends the background loop.
func (r *SSHDPostureReconciler) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// BoxOutcome is what one ReconcileBox pass concluded.
type BoxOutcome struct {
	// Skipped is non-empty when the box was not evaluated (no sshd_config).
	Skipped string
	// Backfilled: first time under policy; drop-in written, no finding.
	Backfilled bool
	// Remediated: the managed drop-in was rewritten this pass.
	Remediated bool
	// Compliant: the effective configuration refuses password login after
	// this pass.
	Compliant bool
	// Finding is non-nil when a finding was raised (or would have been,
	// with no sink configured).
	Finding *threatdetect.Finding
}

// Summary totals one ReconcileAll pass.
type Summary struct {
	Boxes, Skipped, Backfilled, Remediated, Findings, Listeners, Errors int
}

func (s Summary) String() string {
	return fmt.Sprintf("boxes=%d skipped=%d backfilled=%d remediated=%d findings=%d rogue_listeners=%d errors=%d",
		s.Boxes, s.Skipped, s.Backfilled, s.Remediated, s.Findings, s.Listeners, s.Errors)
}

// ReconcileAll runs ReconcileBox over every running, non-core container.
func (r *SSHDPostureReconciler) ReconcileAll(ctx context.Context) Summary {
	var s Summary
	containers, err := r.incus.ListContainers()
	if err != nil {
		log.Printf("[sshd-posture] list containers: %v", err)
		s.Errors++
		return s
	}
	for _, c := range containers {
		if ctx.Err() != nil {
			return s
		}
		if c.State != "Running" || c.Role.IsCoreRole() {
			continue
		}
		s.Boxes++
		if r.listenerCheckEnabled() {
			n, lerr := r.ReconcileListeners(ctx, c.Name)
			if lerr != nil {
				log.Printf("[sshd-posture] %s: listeners: %v", c.Name, lerr)
				s.Errors++
			}
			s.Listeners += n
		}
		out, err := r.ReconcileBox(ctx, c.Name)
		if err != nil {
			// The outcome is still tallied: a pass can remediate and raise
			// a finding and also fail to mark the box.
			log.Printf("[sshd-posture] %s: %v", c.Name, err)
			s.Errors++
		}
		switch {
		case out.Skipped != "":
			s.Skipped++
		case out.Backfilled:
			s.Backfilled++
		}
		if out.Remediated {
			s.Remediated++
		}
		if out.Finding != nil {
			s.Findings++
		}
	}
	return s
}

