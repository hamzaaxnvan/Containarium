//go:build !windows && !containarium_client

// The sentinel command tree (sentinelCmd, defined in sentinel.go) is
// !windows-only; this subcommand attaches to it and carries the same
// constraint.

package cmd

import (
	"fmt"
	"io"
	"log"

	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

var sentinelTunnelIdentityPath string

var sentinelTunnelIdentityCmd = &cobra.Command{
	Use:   "tunnel-identity",
	Short: "Print the pin of the sentinel's tunnel identity",
	Long: `Print the public-key pin of the sentinel's tunnel identity, in the form
tunnel clients take with --sentinel-pin:

  sha256:<64 hex>

The identity is created by the sentinel on its first start (see
--tunnel-tls-identity on 'containarium sentinel'). This command only reads it:
