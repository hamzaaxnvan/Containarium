package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var consoleLog bool

var consoleCmd = &cobra.Command{
	Use:   "console <username>",
	Short: "Read a VM instance's boot-time console log",
	Long: `Read a container's console ring-buffer log.

Unlike SSH, this does not require the instance's own network stack or sshd
to be up — it reads the console device the hypervisor exposes, so it works
even when the instance is stuck at BIOS/UEFI POST, hung in the bootloader,
or has panicked before bringing up networking.

Empty output means either the instance is an LXC container (no serial
console device) or a VM that hasn't produced any console output yet.

Live interactive attach (no --log) requires --http with an http(s)://
--server address — it dials a WebSocket, which a plain gRPC endpoint has
no upgrade path for. To detach without killing the instance, press
<ctrl>+a then q (matching Incus's own console command's convention).

Examples:
  containarium console alice --log
  containarium console alice --http --server https://daemon.example.com`,
