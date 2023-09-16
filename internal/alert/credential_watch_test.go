package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeProber returns a scripted credential source per skill id.
type fakeProber struct {
	mu      sync.Mutex
	targets []CredentialWatchTarget
	sources map[string]pb.CodeCredentialSource
	errs    map[string]error
}

func (p *fakeProber) CredentialWatchTargets(context.Context) ([]CredentialWatchTarget, error) {
	return p.targets, nil
}

func (p *fakeProber) ProbeCredential(_ context.Context, t CredentialWatchTarget) (pb.CodeCredentialSource, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.errs[t.SkillID]; err != nil {
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_UNSPECIFIED, err
	}
	return p.sources[t.SkillID], nil
}

func (p *fakeProber) set(skillID string, src pb.CodeCredentialSource) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sources[skillID] = src
}

// memStateStore is an in-memory CredentialStateStore. Sharing one instance
// across two watchers models a daemon restart against the same Postgres.
type memStateStore struct {
	mu        sync.Mutex
	last      map[string]pb.CodeCredentialSource
	recordErr error
	episodes  map[string]bool
}

func newMemStateStore() *memStateStore {
	return &memStateStore{last: map[string]pb.CodeCredentialSource{}}
}

func (s *memStateStore) LastSeen(_ context.Context, skillID string) (pb.CodeCredentialSource, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.last[skillID]
	return src, ok, nil
}

func (s *memStateStore) Record(_ context.Context, skillID string, src pb.CodeCredentialSource, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	s.last[skillID] = src
	return nil
}

type recordingNotifier struct {
	mu     sync.Mutex
	alerts []CredentialExpiredAlert
}

func (n *recordingNotifier) NotifyCredentialExpired(_ context.Context, a CredentialExpiredAlert) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, a)
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.alerts)
}

const (
	srcInteractive = pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_INTERACTIVE
	srcExpired     = pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_EXPIRED
	srcNone        = pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_NONE
)

func oneTargetProber(src pb.CodeCredentialSource) *fakeProber {
	return &fakeProber{
		targets: []CredentialWatchTarget{{SkillID: "s1", Box: "agent-s1-container", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE}},
		sources: map[string]pb.CodeCredentialSource{"s1": src},
		errs:    map[string]error{},
	}
}

// TestCredentialWatcher_FiresOncePerFlip is AC2's core: a sequence of
// sweeps over one box fires exactly one alert per transition INTO expired,
// and none while it stays expired or while it is anything else.
func TestCredentialWatcher_FiresOncePerFlip(t *testing.T) {
	tests := []struct {
		name      string
		sequence  []pb.CodeCredentialSource
		wantFired int
	}{
		{name: "stays interactive", sequence: []pb.CodeCredentialSource{srcInteractive, srcInteractive, srcInteractive}, wantFired: 0},
		{name: "flip to expired then stays", sequence: []pb.CodeCredentialSource{srcInteractive, srcExpired, srcExpired, srcExpired}, wantFired: 1},
		{name: "expired, re-signed in, expired again", sequence: []pb.CodeCredentialSource{srcInteractive, srcExpired, srcInteractive, srcExpired}, wantFired: 2},
		{name: "first observation already expired fires once", sequence: []pb.CodeCredentialSource{srcExpired, srcExpired}, wantFired: 1},
		{name: "signed out (none) is not expiry", sequence: []pb.CodeCredentialSource{srcInteractive, srcNone, srcNone}, wantFired: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prober := oneTargetProber(tc.sequence[0])
			notifier := &recordingNotifier{}
			w := NewCredentialWatcher(prober, newMemStateStore(), notifier)
			for _, src := range tc.sequence {
				prober.set("s1", src)
				w.Sweep(context.Background())
			}
			if got := notifier.count(); got != tc.wantFired {
				t.Errorf("alerts fired = %d, want %d", got, tc.wantFired)
			}
		})
	}
}

// TestCredentialWatcher_RestartDoesNotRefire: the last-seen state lives in
// the store, not the watcher, so a fresh watcher (a restarted daemon) over
// the same store does not alert again for a box that was already expired.
func TestCredentialWatcher_RestartDoesNotRefire(t *testing.T) {
	prober := oneTargetProber(srcInteractive)
	store := newMemStateStore()
	notifier := &recordingNotifier{}

	first := NewCredentialWatcher(prober, store, notifier)
	first.Sweep(context.Background())
	prober.set("s1", srcExpired)
	first.Sweep(context.Background())

	restarted := NewCredentialWatcher(prober, store, notifier)
	restarted.Sweep(context.Background())
	restarted.Sweep(context.Background())

	if got := notifier.count(); got != 1 {
		t.Fatalf("alerts fired across a restart = %d, want exactly 1", got)
	}
}

// TestCredentialWatcher_AlertCarriesNamesOnly pins the payload: which box,
// which engine, the transition — and no field a credential value could ride.
func TestCredentialWatcher_AlertCarriesNamesOnly(t *testing.T) {
	prober := oneTargetProber(srcInteractive)
	notifier := &recordingNotifier{}
	w := NewCredentialWatcher(prober, newMemStateStore(), notifier)
	fixed := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return fixed }

	w.Sweep(context.Background())
	prober.set("s1", srcExpired)
	w.Sweep(context.Background())

	if notifier.count() != 1 {
		t.Fatalf("alerts fired = %d, want 1", notifier.count())
	}
	got := notifier.alerts[0]
	want := CredentialExpiredAlert{
		SkillID:          "s1",
		Box:              "agent-s1-container",
		Engine:           pb.AgentEngine_AGENT_ENGINE_CLAUDE,
		CredentialSource: srcExpired,
		PreviousSource:   srcInteractive,
		ObservedAt:       fixed,
	}
