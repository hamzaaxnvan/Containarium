//go:build !containarium_client

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2483: with Postgres configured but unreachable at startup, both policy
// stores start fail-closed. The recovery loop retries the durable-store
// install every 5 s, doubling to a 60 s cap, one log line per interval, and
// swaps the Postgres store in when it succeeds. It never installs an
// in-memory store.

// retryClock hands the loop an already-fired timer and records each wait.
// stopAfter > 0 cancels the run on that wait, so the loop ends there.
type retryClock struct {
	mu        sync.Mutex
	waits     []time.Duration
	stopAfter int
	cancel    context.CancelFunc
}

func (c *retryClock) after(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	if c.stopAfter > 0 && len(c.waits) >= c.stopAfter {
		c.cancel()
		return make(chan time.Time) // never fires; ctx.Done wins
	}
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

func (c *retryClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
