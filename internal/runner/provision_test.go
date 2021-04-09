package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBoxes implements BoxManager. Tracks calls so tests can
// assert on what the orchestrator did.
type fakeBoxes struct {
	mu         sync.Mutex
	existing   map[string]bool // box name -> already present
	created    []string
	deleted    []string
	listOut    []string
	failCreate map[string]error
	// assignUsername maps a requested name → the username the daemon
	// "assigns" at create (≠ name), to exercise the control-plane case
	// where SSH must target the assigned username, not the request. (#482)
	assignUsername map[string]string
}

func (f *fakeBoxes) Exists(_ context.Context, name string) (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.existing[name] {
		return false, "", nil
	}
	// Return the assigned username (models the control-plane cld-<uuid>
	// case). Falls back to name when no override is set (OSS daemon). (#482)
	username := name
	if u, ok := f.assignUsername[name]; ok {
		username = u
	}
	return true, username, nil
}

func (f *fakeBoxes) Create(_ context.Context, name, _ string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failCreate[name]; ok {
		return "", "", err
	}
	f.created = append(f.created, name)
	f.existing[name] = true
	// Default: the daemon uses the requested name as the username (OSS
	// daemon behaviour). A test can override via assignUsername to model a
	// control plane that mints a generated username at create. (#482)
	username := name
	if u, ok := f.assignUsername[name]; ok {
		username = u
	}
	return name, username, nil
}

func (f *fakeBoxes) Delete(_ context.Context, name string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	delete(f.existing, name)
	return nil
}

func (f *fakeBoxes) List(_ context.Context) ([]string, error) {
	return f.listOut, nil
}

// fakeInstaller implements RunnerInstaller. Records which boxes
// got an install call and lets tests pre-seed "already installed"
// status to exercise the idempotent path.
type fakeInstaller struct {
	mu               sync.Mutex
	alreadyInstalled map[string]bool
	installed        []string
	failInstall      map[string]error
	gotEnv           map[string]map[string]string
}

func (f *fakeInstaller) IsInstalled(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alreadyInstalled[name], nil
}

func (f *fakeInstaller) Install(_ context.Context, name string, _ []byte, env map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failInstall[name]; ok {
		return err
	}
	f.installed = append(f.installed, name)
	if f.gotEnv == nil {
		f.gotEnv = make(map[string]map[string]string)
	}
	cp := make(map[string]string, len(env))
	for k, v := range env {
		cp[k] = v
	}
	f.gotEnv[name] = cp
	return nil
}

// fakeGitHub implements GitHubAPI. registerOnAttempt[name] = N
// means "ListRunners returns this name starting from the N-th
// call" — lets tests force the polling loop to spin a few times
// before success.
type fakeGitHub struct {
	mu                sync.Mutex
	listed            []string // names visible "now"
	listCalls         int
	registerOnAttempt map[string]int // name -> attempt index from which it becomes visible
	removed           []int64
	listErr           error
	removeErr         error
}

func (f *fakeGitHub) ListRunners(_ context.Context, _, _ string) ([]RegisteredRunner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	visible := append([]string(nil), f.listed...)
	for name, attempt := range f.registerOnAttempt {
		if f.listCalls >= attempt {
			visible = append(visible, name)
		}
	}
	out := make([]RegisteredRunner, 0, len(visible))
	for i, n := range visible {
		out = append(out, RegisteredRunner{ID: int64(i + 1), Name: n, Status: "online"})
	}
	return out, nil
}

func (f *fakeGitHub) RemoveRunner(_ context.Context, _, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, id)
	return nil
}

// fakeClock advances on each Sleep so tests don't actually pause
// and the registration loop hits its deadline deterministically.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}
func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

// -----------------------------------------------------------------
// ValidateOptions
// -----------------------------------------------------------------

func TestValidateOptions(t *testing.T) {
	cases := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{
			name:    "empty repo",
			opts:    Options{Repo: "", PAT: "ghp_x", Count: 1},
			wantErr: "target is required",
		},
		{
			// CHANGED with #1217: a bare owner is no longer malformed, it is
			// an ORGANIZATION target. This case previously asserted the
			// opposite, so it is the one assertion org support genuinely
			// inverts — kept here, flipped, rather than deleted, so the
			// change is visible in the diff.
			name:    "bare owner is an organization target, not an error",
			opts:    Options{Repo: "footprintai", PAT: "ghp_x", Count: 1},
			wantErr: "",
		},
		{
			name:    "malformed target - leading slash",
			opts:    Options{Repo: "/repo", PAT: "ghp_x", Count: 1},
			wantErr: "neither owner/repo nor a bare organization name",
		},
		{
			name:    "malformed target - trailing slash",
			opts:    Options{Repo: "owner/", PAT: "ghp_x", Count: 1},
			wantErr: "neither owner/repo nor a bare organization name",
		},
		{
			name:    "empty PAT",
			opts:    Options{Repo: "owner/repo", PAT: "", Count: 1},
			wantErr: "github_pat is required",
		},
		{
			name:    "count zero",
			opts:    Options{Repo: "owner/repo", PAT: "ghp_x", Count: 0},
			wantErr: "count must be > 0",
		},
		{
			name:    "count negative",
			opts:    Options{Repo: "owner/repo", PAT: "ghp_x", Count: -1},
			wantErr: "count must be > 0",
		},
		{
			name:    "count above cap",
			opts:    Options{Repo: "owner/repo", PAT: "ghp_x", Count: MaxRunnerCount + 1},
			wantErr: "exceeds maximum",
		},
		{
			name:    "valid",
			opts:    Options{Repo: "owner/repo", PAT: "ghp_x", Count: 3},
			wantErr: "",
