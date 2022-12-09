//go:build containarium_client

package cmd

import (
	"os"
	"strings"
	"testing"
)

const clientCommandTreeGolden = "testdata/client_command_tree.golden"

func loadGolden(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(clientCommandTreeGolden)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestClientCommandTree_MatchesGoldenAllowList is #1778's allow-list gate:
// every path registered under containarium_client must be in the checked-in
// golden list, and every approved path must still be registered. A new
