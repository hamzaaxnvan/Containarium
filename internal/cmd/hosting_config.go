//go:build !containarium_client

package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/hosting"
	"github.com/spf13/cobra"
)

var showSecrets bool

var hostingConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "View or manage hosting configuration",
	Long: `View or manage the hosting configuration stored in /etc/containarium/hosting.json.
