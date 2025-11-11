// Package lxc implements box.BoxBackend over the existing LXC/incus
// container.Manager. It is a pure adapter: every method delegates to the
// Manager that the daemon already uses, so behavior is unchanged. The package
// exists to give the daemon a runtime-neutral seam (box.BoxBackend) it can
// hold instead of a concrete *container.Manager — see
// docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md.
package lxc

import (
	"context"
	"strings"

	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Backend adapts a *container.Manager to box.BoxBackend.
type Backend struct {
	mgr *container.Manager
}

// Compile-time assertions: Backend satisfies the core interface and the
// optional capabilities LXC supports.
var (
	_ box.BoxBackend     = (*Backend)(nil)
	_ box.ExecCapable    = (*Backend)(nil)
	_ box.MetricsCapable = (*Backend)(nil)
)

// New returns an LXC backend wrapping the given Manager.
func New(mgr *container.Manager) *Backend {
	return &Backend{mgr: mgr}
}

// Kind reports the LXC substrate.
func (b *Backend) Kind() box.BackendKind { return box.KindLXC }

// Create makes a container matching spec exist and returns its status.
func (b *Backend) Create(_ context.Context, spec box.BoxSpec) (*box.BoxStatus, error) {
	info, err := b.mgr.Create(specToCreateOptions(spec))
	if err != nil {
		return nil, err
	}
	st := StatusFromInfo(info)
	return &st, nil
}

// Start starts a stopped container.
func (b *Backend) Start(_ context.Context, ref box.BoxRef) error {
	return b.mgr.Start(ref.Tenant)
}

// Stop stops a running container.
func (b *Backend) Stop(_ context.Context, ref box.BoxRef, force bool) error {
	return b.mgr.Stop(ref.Tenant, force)
}

// Delete removes a container, stopping it first if running.
func (b *Backend) Delete(_ context.Context, ref box.BoxRef, force bool) error {
	return b.mgr.Delete(ref.Tenant, force)
}

// Get returns the status of a container, or (nil, nil) if it does not exist.
func (b *Backend) Get(_ context.Context, ref box.BoxRef) (*box.BoxStatus, error) {
	info, err := b.mgr.Get(ref.Tenant)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	st := StatusFromInfo(info)
	return &st, nil
}

// List returns the status of every container the Manager sees.
func (b *Backend) List(_ context.Context) ([]box.BoxStatus, error) {
	infos, err := b.mgr.List()
	if err != nil {
		return nil, err
	}
	out := make([]box.BoxStatus, 0, len(infos))
	for i := range infos {
		out = append(out, StatusFromInfo(&infos[i]))
	}
	return out, nil
}

// Resolve reports how an agent reaches the container over SSH. The LXC backend
// knows only the box's direct IP and routing user; the gateway/sentinel host
// is stamped above the seam by the server (from the daemon's --ssh-host flag).
func (b *Backend) Resolve(ctx context.Context, ref box.BoxRef) (*box.BoxEndpoint, error) {
	st, err := b.Get(ctx, ref)
