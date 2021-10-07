package audit

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// SSHCollector periodically reads each box's session log to capture SSH
// login events.
//
// Backend-neutral since #1189: it holds a SessionSource rather than an
// *incus.Client, because K8s boxes keep their session log somewhere else
// (a pod's stderr, not /var/log/auth.log) and write it in a different
// format (dropbear, not OpenSSH).
type SSHCollector struct {
	source   SessionSource
	store    entryLogger
	interval time.Duration
	lastSeen map[string]time.Time // per-box high-water mark
	mu       sync.Mutex
	cancel   context.CancelFunc
}

// NewSSHCollector creates a collector over the LXC session source.
//
// Kept for the existing call site; NewSSHCollectorWithSource is the general
// form.
// entryLogger is the only thing the collector needs from the audit store.
// Narrowed so the collection logic is testable without Postgres — *Store
// satisfies it, so no call site changes.
type entryLogger interface {
	Log(ctx context.Context, entry *AuditEntry) error
}

func NewSSHCollector(incusClient *incus.Client, store *Store) *SSHCollector {
	return NewSSHCollectorWithSource(NewIncusSessionSource(incusClient), store)
}

// NewSSHCollectorWithSource creates a collector over any session source.
func NewSSHCollectorWithSource(source SessionSource, store entryLogger) *SSHCollector {
	return &SSHCollector{
		source:   source,
		store:    store,
		interval: 2 * time.Minute,
		lastSeen: make(map[string]time.Time),
	}
}

// Start begins the background collection loop.
func (sc *SSHCollector) Start(ctx context.Context) {
	ctx, sc.cancel = context.WithCancel(ctx)

	go func() {
		// First run after a short delay
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				sc.collectAll(ctx)
				timer.Reset(sc.interval)
			}
		}
	}()

	log.Printf("SSH login collector started (interval: %v)", sc.interval)
}

