package container

import (
	"context"
	"errors"
	"github.com/footprintai/containarium/pkg/core/incus"
	"strings"
	"testing"
)

type requiredGuard struct{ check func(string, string) error }

func (g requiredGuard) Prepare(context.Context, string, string) error {
	return errors.New("legacy hook must not handle explicit membership")
}
func (g requiredGuard) PrepareNetworkTenant(_ context.Context, name, key string) error {
	return g.check(name, key)
}
func TestNetworkTenantNativeAtBirthAndGuardBeforeAutostart(t *testing.T) {
	b := &guardBackend{stagesBackend: &stagesBackend{}}
	m := NewWithBackend(b)
	m.SetNICGuard(requiredGuard{check: func(name, key string) error {
		if b.started || b.createdConfig.ExtraConfig[incus.TenantLabelKey] != "team-a" || key != "team-a" || b.createdConfig.AutoStart {
			t.Fatal("membership/guard/autostart order violated")
