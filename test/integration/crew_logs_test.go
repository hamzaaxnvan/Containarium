package integration

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCrewLogsHelloCrew runs hello-crew on a real daemon and tails its entry
// member's journal from offset 0 (#2096): the "run started"/"run ended"
// status lines must bracket at least one assistant line.
//
// It needs a daemon whose agent boxes ship agent-runtime and can reach a
// model (a gateway provider key or a seeded provider key), so it self-skips
// unless told the daemon has one:
//
//	CONTAINARIUM_BIN=$(pwd)/bin/containarium \
//	CONTAINARIUM_SERVER=localhost:50051 \
//	CONTAINARIUM_TEST_AGENT_MODEL=1 \
//	  go test -v -run TestCrewLogsHelloCrew ./test/integration/
func TestCrewLogsHelloCrew(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if os.Getenv("CONTAINARIUM_BIN") == "" {
		t.Skip("set CONTAINARIUM_BIN to the containarium binary to run this test")
	}
	if os.Getenv("CONTAINARIUM_TEST_AGENT_MODEL") == "" {
		t.Skip("set CONTAINARIUM_TEST_AGENT_MODEL=1 on a daemon whose agent boxes can reach a model; hello-crew writes no assistant line without one")
	}
	server := getServerAddr(t)
	home := t.TempDir()

	out, err := runQuickstartCLI(t, home, "crew", "run", "hello-crew", "--input", `{"q":"say hi"}`, "--server", server)
	require.NoError(t, err, "crew run failed:\n%s", out)
	m := regexp.MustCompile(`(?m)^Run:\s+(\S+)`).FindStringSubmatch(out)
	require.NotNil(t, m, "no run id in crew run output:\n%s", out)

	logs, err := runQuickstartCLI(t, home, "crew", "logs", m[1], "--follow", "--server", server)
