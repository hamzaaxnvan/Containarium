package security

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// maxConcurrentScans limits parallel scan workers.
const maxConcurrentScans = 1

// SecurityContainerName is the name of the core security container (ZAP lives
// there; the ClamAV scanner no longer does, #2631). Defined here to avoid an
// import cycle with internal/server.
const SecurityContainerName = "containarium-core-security"

// Scanner owns the ClamAV scan job queue and report store.
//
// Scans are never started by the system: no startup cycle, no periodic loop,
// no scan of a newly created box, no scan-all (#2631). A scan runs only because
// a box's owner asked for one, and is meant to execute inside that box with its
// own privileges, never by mounting a tenant's rootfs into a platform
// container. Until the in-box runner exists, ScanContainer records an
// `incomplete` report, never `clean`.
type Scanner struct {
	store    *Store
	box      boxAPI // runs the scan inside the tenant's box
	timeout  time.Duration
	interval time.Duration // retention cleanup cadence
	cancel   context.CancelFunc
	// onScanResult, if set, is called after each completed scan with the
	// container name, owning tenant, and result status (ScanStatusClean |
	// ScanStatusInfected | ScanStatusIncomplete). Only "infected" acts on the
	// quarantine; "clean" and "incomplete" leave it untouched (the verdict is
	// self-reported, #2654). The daemon wires this to the auto-quarantine hook
	// (#659). Kept a plain callback so internal/security keeps no dependency on
	// internal/server.
	onScanResult func(containerName, username, status string)

	// Test seam for ScanContainer (#2589). Nil in production, where the
	// Postgres store is used.
	reports reportSaver // persists the report; nil -> store
}

// reportSaver persists one scan report. *Store satisfies it.
type reportSaver interface {
	SaveReport(ctx context.Context, report *Report) error
}

func (s *Scanner) reportSaver() reportSaver {
	if s.reports != nil {
		return s.reports
	}
	return s.store
}

// SetScanResultHook registers a callback invoked after each completed scan
// (auto-quarantine, #659). Set before Start; nil disables it.
func (s *Scanner) SetScanResultHook(fn func(containerName, username, status string)) {
	s.onScanResult = fn
}

// NewScanner creates a new scanner
func NewScanner(store *Store, box boxAPI) *Scanner {
	return &Scanner{
		store:    store,
		box:      box,
		timeout:  DefaultInBoxScanTimeout,
		interval: 24 * time.Hour,
	}
}

// Start begins the background scanning loop and worker pool
func (s *Scanner) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)

	// Reconcile away any scan-<box> devices leaked by a scan that a prior
	// daemon restart interrupted (#832): ScanContainer removes its mount in a
	// defer, which a kill/restart mid-scan skips. A leaked device's mount pins
	// the target's storage volume (later delete → "dataset is busy" → the TTL
	// sweeper loops forever), and a leaked device whose source was since
	// deleted blocks the security container from starting. No scan survives a
	// restart, so all such devices are stale.
	s.reconcileStaleScanDevices(ctx)

	// Start worker pool
	s.startWorkers(ctx, maxConcurrentScans)

	// Retention cleanup only. Nothing here enqueues a scan (#2631).
	go func() {
		timer := time.NewTimer(5 * time.Minute)
