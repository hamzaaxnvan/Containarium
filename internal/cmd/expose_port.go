package cmd

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/pkg/core/expose"
	"github.com/spf13/cobra"
)

var (
	exposePortPort        int
	exposePortDomain      string
	exposePortDescription string
)

var exposePortCmd = &cobra.Command{
	Use:   "expose-port <username>",
	Short: "Expose a container's port on a public hostname",
	Long: `Resolve a container's current LAN IP and register a domain → IP:port
mapping in the sentinel reverse proxy. After this completes,
https://<domain>/ reaches the container.

This is a friendlier wrapper around 'containarium route add' that
auto-resolves the container's IP from its name. Both this command and
the platform MCP's expose_port tool delegate to the same Go function
in pkg/core/expose so the behavior can never drift.

Example:
  containarium expose-port alice \
      --container-port 8080 \
      --domain blog.example.com \
      --server <host:port>`,
	Args: cobra.ExactArgs(1),
	RunE: runExposePort,
