package ttlsweeper

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakes ---

type fakeIncus struct {
	containers []ContainerView
	err        error
}

func (f *fakeIncus) ListContainers() ([]ContainerView, error) {
	return f.containers, f.err
}

type deleteCall struct {
	name   string
	reason string
}

type fakeDeleter struct {
	mu    sync.Mutex
	calls []deleteCall
	// errs returns one error per call by index. A short slice means
	// later calls fall back to nil (same semantics as autosleep's
	// fakeStopper).
	errs []error
}

func (f *fakeDeleter) DeleteContainer(_ context.Context, name, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.calls)
	f.calls = append(f.calls, deleteCall{name: name, reason: reason})
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return nil
}

func (f *fakeDeleter) recorded() []deleteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deleteCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// names returns the recorded delete-call names sorted, for
// order-independent assertions.
func (f *fakeDeleter) names() []string {
	calls := f.recorded()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.name
	}
	sort.Strings(out)
	return out
}

// --- tests ---

// TestManager_TickDeletesExpiredOnly is the happy path: three
// containers — one expired (with TTL in the past), one active (TTL in
// the future), one with no TTL set. Only the expired one is deleted,
// and the deletion reason includes the expiry timestamp.
func TestManager_TickDeletesExpiredOnly(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Hour)
	futureAt := now.Add(time.Hour)

	inc := &fakeIncus{containers: []ContainerView{
		{Name: "alice-container", TTLExpiresAt: &expiredAt},
		{Name: "bob-container", TTLExpiresAt: &futureAt},
		{Name: "carol-container", TTLExpiresAt: nil},
