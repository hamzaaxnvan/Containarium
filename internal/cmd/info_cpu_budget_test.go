package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// TestRenderCPUBudget: `containarium info` prints the tenant / core /
// physical breakdown and, when the gate is advisory and the host is past
// its ceiling, says so with the ratio (#2284 acceptance criteria 1 and 3).
func TestRenderCPUBudget(t *testing.T) {
	t.Run("nil budget prints nothing", func(t *testing.T) {
