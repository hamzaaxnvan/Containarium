//go:build !windows

package hostcheck

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The platform's own daemons must keep scheduling when tenants contend
// (#2284). The check reads the EFFECTIVE weight systemd realized for each
// unit's cgroup — not the unit file — so a drop-in that was written but
// never applied (no daemon-reload, no restart) shows up red.
func TestPlatformCPUWeightCheck(t *testing.T) {
	weight := strconv.Itoa(PlatformCPUWeight)
	cases := map[string]struct {
		files  map[string]string // relative to the system.slice dir
		wantOK bool
		detail []string
	}{
		"both units at the platform weight": {
			files:  map[string]string{"incus.service/cpu.weight": weight + "\n", "containarium.service/cpu.weight": weight + "\n"},
			wantOK: true,
			detail: []string{"incus.service=" + weight, "containarium.service=" + weight},
		},
		"above the floor is fine": {
			files:  map[string]string{"incus.service/cpu.weight": "5000\n", "containarium.service/cpu.weight": weight + "\n"},
			wantOK: true,
		},
		"incus still at the tenant default": {
			files:  map[string]string{"incus.service/cpu.weight": "100\n", "containarium.service/cpu.weight": weight + "\n"},
			wantOK: false,
			detail: []string{"incus.service=100", "below " + weight},
		},
		"daemon unit cgroup missing": {
			files:  map[string]string{"incus.service/cpu.weight": weight + "\n"},
			wantOK: false,
			detail: []string{"could not determine", "containarium.service"},
		},
		"nothing at all": {
			files:  map[string]string{},
			wantOK: false,
			detail: []string{"could not determine"},
		},
		"garbage is unknown, not a pass": {
			files:  map[string]string{"incus.service/cpu.weight": "max\n", "containarium.service/cpu.weight": weight + "\n"},
			wantOK: false,
			detail: []string{"could not determine", "incus.service"},
		},
	}
