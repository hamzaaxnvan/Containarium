package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSandboxBackend is a minimal, map-backed incus.Backend fake: real
// enough that GetContainer after CreateContainer sees the labels/tenant
// the create call stamped, without depending on a live Incus daemon.
//
// mu guards instances/deleteCalls: pool.Reconcile warms multiple members
// for the same template concurrently (one goroutine per warm, see
// pool.go's Reconcile), so any test with min_warm > 1 drives concurrent
// CreateContainer/StartContainer calls into this single fake — without a
// lock that's a genuine, `-race`-catchable data race, not just a
// theoretical one.
type fakeSandboxBackend struct {
	incus.Backend
	mu        sync.Mutex
	instances map[string]incus.ContainerInfo

	waitNetworkErr error
	execStdout     string
	execStderr     string
	execExitCode   int
	execErr        error
	// execCalls records every command this fake was asked to run, in order —
	// (containerName, command) — so tests can assert not just the outcome of
	// an Exec but the SEQUENCE of commands issued (cloud#1733's fix needs a
	// kill-then-launch order, which a fixed stdout/exit-code alone can't pin).
	execCalls          []fakeExecCall
	writeFileErr       error
	readFileContent    []byte
	readFileErr        error
	deleteContainerErr error
	deleteCalls        int
	startErr           error
	setLabelsErr       error
	setConfigErr       error
}

func newFakeSandboxBackend() *fakeSandboxBackend {
	return &fakeSandboxBackend{instances: make(map[string]incus.ContainerInfo)}
}

func (b *fakeSandboxBackend) CreateContainer(c incus.ContainerConfig) error {
	info := incus.ContainerInfo{
		Name:   c.Name,
		State:  "Stopped",
		Labels: extractTestLabels(c.ExtraConfig),
		Tenant: c.ExtraConfig[incus.TenantLabelKey],
		Image:  c.Image,
	}
	if raw := c.ExtraConfig[incus.TTLExpiresAtKey]; raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			info.TTLExpiresAt = t
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.instances[c.Name] = info
	return nil
}

// ListContainers returns every tracked instance, mirroring the real
// backend closely enough for ttlsweeperSandboxAdapter's tests: it reads
// exactly (Name, Labels, TTLExpiresAt), all of which CreateContainer/
// SetConfig/SetLabels already populate above.
func (b *fakeSandboxBackend) ListContainers() ([]incus.ContainerInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]incus.ContainerInfo, 0, len(b.instances))
	for _, info := range b.instances {
		out = append(out, info)
	}
	return out, nil
}

// extractTestLabels mirrors incus's own LabelPrefix-stripping so the fake's
// GetContainer sees the same view a real backend would.
func extractTestLabels(config map[string]string) map[string]string {
	labels := make(map[string]string)
	for k, v := range config {
		if len(k) > len(incus.LabelPrefix) && k[:len(incus.LabelPrefix)] == incus.LabelPrefix {
			labels[k[len(incus.LabelPrefix):]] = v
		}
	}
	return labels
}

func (b *fakeSandboxBackend) StartContainer(name string) error {
	if b.startErr != nil {
		return b.startErr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if info, ok := b.instances[name]; ok {
		info.State = "Running"
		b.instances[name] = info
	}
	return nil
}

func (b *fakeSandboxBackend) StopContainer(string, bool) error { return nil }

func (b *fakeSandboxBackend) DeleteContainer(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleteCalls++
	if b.deleteContainerErr != nil {
		return b.deleteContainerErr
	}
	delete(b.instances, name)
	return nil
}

func (b *fakeSandboxBackend) WaitForNetwork(string, time.Duration) (string, error) {
	if b.waitNetworkErr != nil {
		return "", b.waitNetworkErr
	}
	return "203.0.113.9", nil
}

func (b *fakeSandboxBackend) GetContainer(name string) (*incus.ContainerInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.instances[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return &info, nil
}

