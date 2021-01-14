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
		}
		return nil
	}})
	opts := stagesOpts()
	opts.NetworkTenant = "team-a"
	opts.AutoStart = true
	if _, err := m.Create(opts); err != nil {
		t.Fatal(err)
	}
	if !b.started || b.config["boot.autostart"] != "true" {
		t.Fatal("guarded instance did not regain requested lifecycle")
	}
}
func TestNetworkTenantFailsClosedWithoutRequiredHook(t *testing.T) {
	for _, g := range []NICGuard{nil, &recordingGuard{}, requiredGuard{check: func(string, string) error { return errors.New("unsupported") }}} {
		b := &guardBackend{stagesBackend: &stagesBackend{}}
		m := NewWithBackend(b)
		m.SetNICGuard(g)
		opts := stagesOpts()
		opts.NetworkTenant = "team-a"
		if _, err := m.Create(opts); !errors.Is(err, ErrNICGuard) {
			t.Fatalf("wanted closed failure, got %v", err)
		}
		if b.started || b.deleteCalls != 1 {
