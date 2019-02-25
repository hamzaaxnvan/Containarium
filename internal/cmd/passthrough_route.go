package cmd

import (
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// passthroughRouteCmd represents the passthrough-route command group.
//
// Named distinctly from the existing `passthrough` command (local iptables
// DNAT on the host running the CLI, pkg/core/network — see passthrough.go):
