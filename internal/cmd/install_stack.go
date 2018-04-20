package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var installStackCmd = &cobra.Command{
	Use:   "install-stack <username> <stack-id>",
	Short: "Install a software stack on a running container",
	Long: `Install a pre-configured software stack or base script on an existing running container.

Stacks are developer tool bundles (nodejs, python, golang, etc.) whose post-install
commands run as the container user. Base scripts are system-level scripts
(e.g. ntp) that run as root.

Examples:
  # Install Node.js on alice's container
