//go:build !containarium_client

package server

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// stopTestUser is the OS user the tests run as: StopBoxRun refuses to signal a
// process the box user does not own, and the test processes belong to us.
func stopTestUser(t *testing.T) string {
	t.Helper()
	hostBootID(t) // skips where there is no /proc
	u, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	return u.Username
}

// newFakeEngine is a fake ~/.local/bin/claude running body.
func newFakeEngine(t *testing.T, body string) *fakeBoxUser {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &fakeBoxUser{home: home}
}

// startLongRun starts a run whose fake engine runs body. A body containing the token
// {{READY}} says where the engine marks itself ready (after any trap it installs); the
// helper then waits for that mark, because the run wrapper probes the toolchain before
// it starts the engine and a stop that lands first would kill the wrapper before the
// engine exists.
func startLongRun(t *testing.T, body string) (*ContainerServer, string, *pb.BoxRun) {
	t.Helper()
