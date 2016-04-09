package network

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// PassthroughSyncJob synchronizes passthrough routes from PostgreSQL (source of truth) to iptables (runtime)
type PassthroughSyncJob struct {
	store    PassthroughStore
	manager  *PassthroughManager
	interval time.Duration

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewPassthroughSyncJob creates a new passthrough sync job
func NewPassthroughSyncJob(store PassthroughStore, manager *PassthroughManager, interval time.Duration) *PassthroughSyncJob {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	return &PassthroughSyncJob{
		store:    store,
		manager:  manager,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins the background sync job
func (j *PassthroughSyncJob) Start(ctx context.Context) {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return
	}
	j.running = true
	j.stopCh = make(chan struct{})
	j.doneCh = make(chan struct{})
	j.mu.Unlock()

	go j.run(ctx)
}

// Stop stops the background sync job
func (j *PassthroughSyncJob) Stop() {
	j.mu.Lock()
	if !j.running {
		j.mu.Unlock()
		return
	}
	j.mu.Unlock()

	close(j.stopCh)
	<-j.doneCh

	j.mu.Lock()
	j.running = false
	j.mu.Unlock()
}

// SyncNow triggers an immediate sync
func (j *PassthroughSyncJob) SyncNow(ctx context.Context) error {
	return j.sync(ctx)
}

// run is the main loop for the background sync job
