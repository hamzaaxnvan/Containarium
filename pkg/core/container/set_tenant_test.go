package container

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

type tenantBackend struct {
	incus.Backend
	name, key, value string
}

func (b *tenantBackend) UpdateContainerConfig(name, key, value string) error {
	b.name, b.key, b.value = name, key, value
	return nil
}

// SetTenant writes the explicit-owner config key on the named container —
// by container name, since the owner is exactly what no longer matches
// the name (#2199).
