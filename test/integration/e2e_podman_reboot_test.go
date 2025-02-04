package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2EPodmanRebootSurvival validates the #387 reboot-durability chain
// end-to-end on a real GCE host:
//
//	host reboot
//	  → Incus autostarts the tenant container (boot.autostart=true)
//	    → systemd in the container starts podman-restart.service
//	      → the workload (created --restart=always) comes back RUNNING
//
// The daemon's provisioning of this chain (system + user
// podman-restart.service + enable-linger) is unit-covered in
// pkg/core/container/podman_restart_test.go; this test exercises the OS
// path the unit test can't — that a real reboot actually brings the
// workload back without intervention. It is the "reboot-survival case in
// the acceptance suite" referenced by docs/PODMAN-REBOOT-DURABILITY.md.
//
// Rootful is used here (system podman-restart.service) to keep the test
// self-contained; the rootless linger path is the harder one the daemon
// wires and is asserted at the unit level.
//
