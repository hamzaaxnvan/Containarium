// `containarium security clamav install <box>` — install ClamAV and its
// signatures inside one box so the tenant-run malware scan can run (#2659).
// CLI-first: it calls the same mcp.Client.InstallClamav the install_clamav MCP
// tool uses.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var securityClamavCmd = &cobra.Command{
	Use:   "clamav",
