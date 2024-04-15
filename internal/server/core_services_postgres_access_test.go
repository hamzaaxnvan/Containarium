package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

func TestPostgresHBALines(t *testing.T) {
	t.Run("daemon and grafana only, each a /32, scram, tagged", func(t *testing.T) {
		got, err := postgresHBALines("containarium", "10.100.0.1", "10.100.0.239")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d lines, want 2: %v", len(got), got)
		}
		want := []struct{ db, ip string }{{"all", "10.100.0.1/32"}, {"grafana", "10.100.0.239/32"}}
		for i, w := range want {
			f := strings.Fields(got[i])
			// host <db> <user> <addr> <method> # tag
			if len(f) < 5 || f[0] != "host" || f[1] != w.db || f[2] != "containarium" || f[3] != w.ip || f[4] != "scram-sha-256" {
				t.Errorf("line %d = %q, want host %s containarium %s scram-sha-256", i, got[i], w.db, w.ip)
			}
			if !strings.HasSuffix(got[i], hbaMarker) {
				t.Errorf("line %d is not tagged %q, so the fallback could not find it: %q", i, hbaMarker, got[i])
			}
		}
	})
	t.Run("no grafana address → the daemon line only", func(t *testing.T) {
		got, err := postgresHBALines("containarium", "10.100.0.1", "")
		if err != nil || len(got) != 1 {
			t.Fatalf("lines=%v err=%v; want exactly the daemon line", got, err)
		}
	})
	t.Run("a subnet-wide rule is never produced", func(t *testing.T) {
		got, _ := postgresHBALines("containarium", "10.100.0.1", "10.100.0.239")
		for _, l := range got {
			if strings.Contains(l, "/24") || strings.Contains(l, "/16") || strings.Contains(l, "0.0.0.0") {
				t.Errorf("line allows more than one address: %q", l)
			}
		}
	})
	t.Run("values that reach a shell are validated", func(t *testing.T) {
		for _, tc := range []struct{ name, user, daemon, grafana string }{
			{"empty daemon address", "containarium", "", "10.100.0.239"},
			{"daemon not an IP", "containarium", "10.100.0.1; rm -rf /", ""},
			{"daemon is IPv6", "containarium", "fe80::1", ""},
			{"grafana not an IP", "containarium", "10.100.0.1", "x' >> /etc/passwd #"},
			{"user with a quote", "o'brien", "10.100.0.1", ""},
			{"user with a space", "two words", "10.100.0.1", ""},
			{"empty user", "", "10.100.0.1", ""},
		} {
			if got, err := postgresHBALines(tc.user, tc.daemon, tc.grafana); err == nil {
				t.Errorf("%s: expected an error, got lines %v", tc.name, got)
			}
		}
	})
}

// accessBox records every command run in the Postgres container and serves a
// scripted metrics container.
type accessBox struct {
	*incustest.MockBackend
	execs []string
}

func newAccessBox(vm *incus.ContainerInfo) *accessBox {
	b := &accessBox{MockBackend: incustest.NewMockBackend()}
	if vm != nil {
		b.Containers[CoreVictoriaMetricsContainer] = vm
	}
	b.ExecFunc = func(_ string, cmd []string) error {
		b.execs = append(b.execs, strings.Join(cmd, " "))
		return nil
	}
	return b
}

func (b *accessBox) ran(substr string) int {
	n := 0
	for _, e := range b.execs {
		if strings.Contains(e, substr) {
			n++
		}
