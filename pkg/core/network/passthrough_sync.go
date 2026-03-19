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

