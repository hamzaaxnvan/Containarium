//go:build !containarium_client

package cmd

import (
	"os"
	"strings"
	"testing"
)

// TestContainariumdCommandTree_SupersetOfClientGolden is #1778's
// untagged-build parity gate: the split must never *lose* a command from
// the server binary. containariumd's tree is asserted to be a strict
// superset of the client's golden list, once every "moved:" entry is
// resolved to what it actually means in containariumd:
//
//   - a moved name that is itself runnable server-side (daemon, sentinel,
//     tunnel, …) must appear as-is;
//   - a moved name that is a group-only parent in containariumd (audit,
//     cloud, hosting, …, no RunE of its own — only its subcommands are
//     runnable) is satisfied by at least one "<name> <child>" path
//     existing, since renderCommandTree only lists runnable leaves and a
//     bare group name is never one.
//
// Every other (non-"moved:") golden entry is a real client command that
// containariumd carries unchanged (the design's superset guarantee), so it
// must appear byte-for-byte.
func TestContainariumdCommandTree_SupersetOfClientGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/client_command_tree.golden")
	if err != nil {
		t.Fatalf("read golden file: %v", err)
