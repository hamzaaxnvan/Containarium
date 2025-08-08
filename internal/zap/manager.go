package zap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/footprintai/containarium/internal/app"
)

const maxConcurrentWorkers = 2 // ZAP scans are heavy, limit concurrency

// ManagerConfig holds configuration for the ZAP manager
type ManagerConfig struct {
	Interval time.Duration // scan interval (default 30 days)
}

// ErrZapNotInstalled is returned when a scan is requested on a host where ZAP
// has not been installed. Scans are rejected up front rather than enqueued
// to fail repeatedly in the workers.
var ErrZapNotInstalled = errors.New("ZAP is not installed in the security container (run the InstallZap RPC/API for this host first)")

// Manager orchestrates periodic ZAP scans using a PostgreSQL job queue
type Manager struct {
	store      *Store
	routeStore *app.RouteStore
	scanner    *Scanner
	config     ManagerConfig
	cancel     context.CancelFunc

	// zapInstalled reports whether ZAP is installed; overridable in tests.
	zapInstalled func() bool
}

// NewManager creates a new ZAP manager
func NewManager(
	store *Store,
	routeStore *app.RouteStore,
	config ManagerConfig,
) *Manager {
	if config.Interval == 0 {
		config.Interval = 30 * 24 * time.Hour // 30 days
	}

	m := &Manager{
		store:      store,
		routeStore: routeStore,
		scanner:    NewScanner(),
		config:     config,
	}
	m.zapInstalled = m.scanner.Available
	return m
}

// Start begins the worker pool and periodic scan enqueue loop
func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)

	// Start worker goroutines
	m.startWorkers(ctx, maxConcurrentWorkers)

	// Start periodic scan cycle loop
	go m.scanCycleLoop(ctx)

	log.Printf("ZAP manager started (interval: %v, workers: %d)", m.config.Interval, maxConcurrentWorkers)
}

// Stop stops the worker pool and scan loop
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	log.Printf("ZAP manager stopped")
}

// ZapAvailable returns whether ZAP is installed
func (m *Manager) ZapAvailable() bool {
	return m.scanner.Available()
}

// ZapVersion returns the ZAP version string
func (m *Manager) ZapVersion() string {
	return m.scanner.Version()
}

// Interval returns the configured scan interval
func (m *Manager) Interval() time.Duration {
	return m.config.Interval
}

// scanCycleLoop runs periodic scan enqueue cycles
func (m *Manager) scanCycleLoop(ctx context.Context) {
	// Startup delay — give the system time to settle
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.runScanCycle(ctx)
			timer.Reset(m.config.Interval)
		}
	}
}

// runScanCycle creates a scan run and enqueues jobs for all targets
func (m *Manager) runScanCycle(ctx context.Context) {
	scanRunID, targetsCount, err := m.enqueueScan(ctx, "scheduled", "")
	if errors.Is(err, ErrZapNotInstalled) {
		log.Printf("ZAP scan cycle: skipped: %v", err)
		return
	}
	if err != nil {
		log.Printf("ZAP scan cycle: failed to enqueue: %v", err)
		return
	}

	// Cleanup old data (90 day retention)
	if err := m.store.Cleanup(ctx, 90); err != nil {
		log.Printf("ZAP scan cycle: cleanup failed: %v", err)
	}
	if err := m.store.CleanupOldJobs(ctx, 90); err != nil {
		log.Printf("ZAP scan cycle: job cleanup failed: %v", err)
	}

	log.Printf("ZAP scan cycle: %d jobs enqueued for scan run %s", targetsCount, scanRunID)
}

// enqueueScan creates a scan run record, collects targets, and enqueues a job per target.
// containerName scopes the scan to a single container (filters route targets to
// that container's routes) and is recorded on the run. Empty = cluster-wide.
func (m *Manager) enqueueScan(ctx context.Context, trigger, containerName string) (string, int, error) {
	// Pre-flight: do not create a run or enqueue jobs that cannot succeed.
	if !m.zapInstalled() {
		return "", 0, ErrZapNotInstalled
	}
	scanRunID, err := m.store.CreateScanRun(ctx, trigger, containerName)
	if err != nil {
