//go:build incus

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Which `incus` CLI commands the migration runner uses actually exist.
//
// Found the hard way: #1390's test called `incus snapshot <c> <name>` — copied
// from pkg/core/incus/migration.go, which is the PRODUCTION migration runner —
// and Incus 6.0.0 answered:
//
//	Error: unknown command "reg19801-container" for "incus snapshot"
//
// `incus snapshot` is a command GROUP in Incus 6.x, not a verb. Every
// MoveContainer test substitutes a fake MigrationRunner, and dual_server.go
// wires the real ExecRunner only in production — so these six shell-outs have
// never run against a real Incus in CI, and at least one of them cannot work.
//
// This file probes each command against a real Incus and records the answer,
// so the fix is designed against demonstrated behaviour rather than against a
// plausible reading of the CLI docs. Same ordering that turned #1160c's
// premise from plausible into disproved (#1384) — applied here to a defect in
// shipped code rather than to a design.
//
// It calls the PRODUCTION ExecRunner methods rather than probing raw argv.
// Probing argv would only prove that some syntax works, while leaving
// ExecRunner free to use a different one — which is precisely the gap that let
// #1392 ship. What needs testing is the function the daemon actually calls.
//
// probeCandidates below survives for the one case that still needs it: #1390's
// test has to produce an Incus-level snapshot across Incus versions without
// depending on the runner it is investigating.

// incusRun runs the incus CLI and returns combined output plus whether it
// succeeded, without failing the test — a non-zero exit is data here.
func incusRun(args ...string) (string, bool) {
	out, err := exec.Command("incus", args...).CombinedOutput() // #nosec G204 -- literal args below
	return strings.TrimSpace(string(out)), err == nil
}

// probeCandidates tries each candidate argv for one operation and returns the
// first that worked, or "" if none did.
func probeCandidates(t *testing.T, op string, candidates [][]string) []string {
	t.Helper()
	for _, argv := range candidates {
		out, ok := incusRun(argv...)
		if ok {
			t.Logf("FACT [%s]: `incus %s` works", op, strings.Join(argv, " "))
			return argv
		}
		t.Logf("  [%s] `incus %s` failed: %s", op, strings.Join(argv, " "), firstLine(out))
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestIntegrationIncus_MigrationRunnerCLISurface exercises every command
// pkg/core/incus/migration.go shells out to, against a real Incus.
//
