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
