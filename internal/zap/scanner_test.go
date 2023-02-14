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
	s := NewScanner()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := s.EnsureDaemonRunning(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when ZAP is not installed, got nil")
