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
