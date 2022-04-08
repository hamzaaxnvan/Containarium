package container

import (
	"errors"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
)

// #1530: the benchmark investigation found installPackages waited a fixed
// 5s "for cloud-init to finish" on every create, even against
// images:ubuntu/24.04 — this daemon's default create base — which doesn't
// ship cloud-init at all. Pure waste on every stackless create. These tests
// cover cloudInitPresent directly, and installPackages' use of it.

// cloudInitProbeBackend fakes exactly the two calls installPackages makes
// before this fix's guard point: the cloud-init presence probe
// (ExecWithExitCode) and, immediately after, a package-repo update (Exec) —
// deliberately failing that second call so installPackages returns right
// after the sleep-or-skip decision, without needing to fake the rest of a
// full package install.
type cloudInitProbeBackend struct {
	incus.Backend
	probeExitCode int
	probeErr      error
}

func (b *cloudInitProbeBackend) ExecWithExitCode(_ string, _ []string) (string, string, int, error) {
	return "", "", b.probeExitCode, b.probeErr
}

