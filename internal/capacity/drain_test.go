package capacity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeStopper records every StopWorkload call and lets a test script per-call
// behavior (error once, error always) so the drainer's graceful→force fallback
// and failure recording are observable.
type fakeStopper struct {
	mu    sync.Mutex
	calls []stopCall
	// failGraceful: usernames whose non-forced stop returns an error (the
	// drainer should then retry with force).
	failGraceful map[string]bool
	// failAll: usernames whose stop always errors (records into Failed).
	failAll map[string]bool
}

type stopCall struct {
	username string
	force    bool
}

func (f *fakeStopper) StopWorkload(_ context.Context, username string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, stopCall{username: username, force: force})
	if f.failAll[username] {
		return errors.New("boom")
	}
	if !force && f.failGraceful[username] {
		return errors.New("graceful refused")
	}
	return nil
}

func (f *fakeStopper) callsFor(username string) []stopCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []stopCall
	for _, c := range f.calls {
		if c.username == username {
			out = append(out, c)
		}
	}
	return out
}

func cand(name, user string) DrainCandidate {
	return DrainCandidate{ContainerName: name, Username: user}
}

func TestDrainAllGraceful(t *testing.T) {
	fs := &fakeStopper{}
	d := NewDrainer(fs)
	res, skipped := d.Drain(context.Background(),
		[]DrainCandidate{cand("a-container", "a"), cand("b-container", "b")},
		time.Minute)
	if skipped {
		t.Fatal("unexpected skip on first drain")
	}
	if len(res.Drained) != 2 {
