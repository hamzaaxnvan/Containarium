package zap

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestEnsureDaemonRunning_FailsFastWhenNotInstalled locks down the fix for
// #960: EnsureDaemonRunning must check Scanner.Available() (which delegates
// to Installer.IsInstalled(), an `incus exec ... test -x .../zap.sh` shell
// call) before attempting to start the daemon, and return immediately with
// a clear error instead of backgrounding a doomed start command and burning
// the full 120-second readiness poll.
//
// No incus daemon or security container exists in the CI/test environment,
// so IsInstalled() naturally reports "not installed" here (the underlying
// `incus` binary is either absent or the container doesn't exist) — this
// exercises the real code path end to end rather than mocking it.
func TestEnsureDaemonRunning_FailsFastWhenNotInstalled(t *testing.T) {
