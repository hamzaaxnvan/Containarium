package container

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
)

// stagesBackend fakes exactly what Create touches on the baked-image path.
// It embeds incus.Backend (nil) for the rest of the interface, so a call to
// anything unimplemented panics and names the gap.
type stagesBackend struct {
	incus.Backend
	waitNetworkErr  error
	onWaitNetwork   func() // called on entry to WaitForNetwork, when set
	deleteCalls     int    // DeleteContainer invocations (cleanup evidence)
	createdConfig   incus.ContainerConfig
	setLabelsCalled bool
	config          map[string]string // SetConfig writes, by key
}

func (b *stagesBackend) GetImageAliasProperties(string) (map[string]string, bool, error) {
	// A baked image matching Create's default image + podman=false, so the
	// test skips installPackages (which sleeps 5s waiting for cloud-init).
	return map[string]string{
		bakedPropBaked:  "true",
		bakedPropSource: "images:ubuntu/24.04",
		bakedPropPodman: strconv.FormatBool(false),
	}, true, nil
}

func (b *stagesBackend) CreateContainer(c incus.ContainerConfig) error {
	b.createdConfig = c
	return nil
}
func (b *stagesBackend) StartContainer(string) error      { return nil }
func (b *stagesBackend) StopContainer(string, bool) error { return nil }
func (b *stagesBackend) DeleteContainer(string) error     { b.deleteCalls++; return nil }
func (b *stagesBackend) SetLabels(string, map[string]string) error {
	b.setLabelsCalled = true
	return nil
}
func (b *stagesBackend) SetConfig(_, key, value string) error {
	if b.config == nil {
		b.config = map[string]string{}
	}
	b.config[key] = value
	return nil
}
func (b *stagesBackend) Exec(string, []string) error { return nil }
func (b *stagesBackend) ExecWithExitCode(string, []string) (string, string, int, error) {
	return "", "", 1, nil // no cloud-init: the stack-install path skips its wait
}
func (b *stagesBackend) WriteFile(string, string, []byte, string) error {
	return nil
}

func (b *stagesBackend) WaitForNetwork(string, time.Duration) (string, error) {
	if b.onWaitNetwork != nil {
		b.onWaitNetwork()
	}
	if b.waitNetworkErr != nil {
		return "", b.waitNetworkErr
	}
	return "203.0.113.7", nil
}

func (b *stagesBackend) GetContainer(name string) (*incus.ContainerInfo, error) {
	return &incus.ContainerInfo{Name: name}, nil
}

// stageLog records every observation in order. Mutex-guarded because the
// jump-account stage reports from its own goroutine.
type stageLog struct {
	mu        sync.Mutex
	stages    []CreateStage
	durations map[CreateStage]time.Duration
}

func newStageLog() *stageLog {
	return &stageLog{durations: make(map[CreateStage]time.Duration)}
}

// snapshot copies the log under its lock. The jump-account stage reports from
// its own goroutine, and since #2489 that goroutine also runs for creates with
// no SSH keys, so tests must not read stages/durations directly.
func (l *stageLog) snapshot() ([]CreateStage, map[CreateStage]time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stages := append([]CreateStage(nil), l.stages...)
	durs := make(map[CreateStage]time.Duration, len(l.durations))
	for k, v := range l.durations {
		durs[k] = v
	}
	return stages, durs
}

func (l *stageLog) observer() StageObserver {
	return func(s CreateStage, d time.Duration) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.stages = append(l.stages, s)
		l.durations[s] = d
	}
}

func stagesOpts() CreateOptions {
	return CreateOptions{
		Username: "alice",
		Image:    "images:ubuntu/24.04",
		// No SSHKeys: the jump account is still created, keyless (#2489); the
		// package TestMain stubs the host useradd so a unit test never runs it.
	}
}

// TestCreate_ObservesStagesInOrder pins the Phase 0 instrumentation contract
// (docs/architecture/two-digit-ms-sandbox-spawn.md): every stage that runs is
// observed, in execution order, and stages that are skipped — baked image,
// no git source — produce no observation at all. The jump account is NOT
// skipped without SSH keys (#2489): it runs keyless.
func TestCreate_ObservesStagesInOrder(t *testing.T) {
	// Keep getJumpServerSSHKey from finding a real key in $HOME, which would
	// nondeterministically add an add_ssh_keys stage on developer machines.
	t.Setenv("HOME", t.TempDir())

	log := newStageLog()
	m := NewWithBackend(&stagesBackend{})
	m.SetStageObserver(log.observer())

	if _, err := m.Create(stagesOpts()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Every stage this create actually runs, in path order. add_ssh_keys is
	// excluded: it depends on whether the host has a jump-server key, which
	// the HOME override above pins to "no".
	want := []CreateStage{
		StageCreateInstance,
		StageStartInstance,
		StageWaitNetwork,
		StageCreateUser,
		StageGetInfo,
		StageTotal,
	}
	stages, durations := log.snapshot()
	assertSubsequence(t, stages, want)

	// The jump account runs concurrently with the guest stages, so WHEN it is
	// observed is not deterministic and it stays out of the ordered list; that
	// it ran (keyless, #2489) is the contract.
	if _, ok := durations[StageJumpAccount]; !ok {
		t.Errorf("jump_account stage not observed on a create with no SSH keys (observed: %v)", stages)
	}

	for _, absent := range []CreateStage{StageInstallPackages, StageGitSource, StageAddSSHKeys} {
		if _, ok := durations[absent]; ok {
			t.Errorf("stage %q was observed but should have been skipped (observed: %v)", absent, stages)
		}
	}

	for stage, d := range durations {
		if d < 0 {
			t.Errorf("stage %q recorded negative duration %v", stage, d)
		}
	}
	if durations[StageTotal] < durations[StageStartInstance] {
		t.Errorf("total (%v) must cover start_instance (%v)", durations[StageTotal], durations[StageStartInstance])
	}
}

// TestCreate_NilObserverIsNoop — the default: no observer, no panic, same
// create.
func TestCreate_NilObserverIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := NewWithBackend(&stagesBackend{})
	if _, err := m.Create(stagesOpts()); err != nil {
		t.Fatalf("Create without observer: %v", err)
	}
}

// TestCreate_FailedStageIsStillObserved pins the documented semantics: a
// stage that errors is still recorded (its duration is real — a network wait
// that burns its timeout is exactly the sample worth keeping), while total is
// recorded only on success.
func TestCreate_FailedStageIsStillObserved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	log := newStageLog()
	m := NewWithBackend(&stagesBackend{waitNetworkErr: errors.New("no lease")})
	m.SetStageObserver(log.observer())

	if _, err := m.Create(stagesOpts()); err == nil {
		t.Fatal("Create should fail when WaitForNetwork fails")
	}

	stages, durations := log.snapshot()
	if _, ok := durations[StageWaitNetwork]; !ok {
		t.Errorf("failed wait_network stage was not observed (observed: %v)", stages)
	}
	if _, ok := durations[StageTotal]; ok {
		t.Errorf("total must not be observed on a failed create (observed: %v)", stages)
	}
}

// TestCreate_LabelsRideInTheCreateRequest pins the fold: labels (including
// the OS-type label) arrive in CreateContainer's own ExtraConfig, and
// SetLabels is never called from Create — there is no longer a separate
