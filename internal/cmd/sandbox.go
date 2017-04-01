package cmd

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Ephemeral, no-SSH sandboxes (#1488 Phase 1: the two-digit-ms spawn
// path's cold-path implementation). Access is exclusively these five
// verbs — there is no `containarium sandbox ssh`.

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage ephemeral, no-SSH sandboxes",
	Long: `Spawn, exec into, transfer files to/from, and delete ephemeral sandboxes.

A sandbox has no per-tenant Linux account and no SSH — these verbs are its
entire access surface. Phase 1 is the cold path: every spawn creates a
fresh container (the same floor as a persistent box minus identity
seeding). See docs/architecture/two-digit-ms-sandbox-spawn.md.

  containarium sandbox spawn --server <host>
  containarium sandbox exec <sandbox-id> --server <host> -- echo hello
  containarium sandbox write-file <sandbox-id> /workspace/in.txt --from ./local.txt --server <host>
  containarium sandbox read-file <sandbox-id> /workspace/out.txt --server <host>
  containarium sandbox delete <sandbox-id> --server <host>
  containarium sandbox pool-status --server <host>`,
}

var (
	sandboxIdleTTLSeconds int32
	sandboxAllowCold      bool
	sandboxWriteFrom      string
	sandboxWriteMode      string
	sandboxReadTo         string
)

var sandboxSpawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Spawn a sandbox",
	Args:  cobra.NoArgs,
	RunE:  runSandboxSpawn,
}

var sandboxExecCmd = &cobra.Command{
	Use:   "exec <sandbox-id> -- <command...>",
	Short: "Run a command inside a sandbox",
	Long: `Run a command inside a sandbox and print its stdout/stderr.

Exits with the REMOTE command's exit code (not a generic CLI error code),
so this composes in scripts the way a local command would:

  containarium sandbox exec sandbox-abc123 --server <host> -- true; echo $?`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSandboxExec,
}

var sandboxWriteFileCmd = &cobra.Command{
	Use:   "write-file <sandbox-id> <remote-path>",
	Short: "Write a local file into a sandbox",
	Args:  cobra.ExactArgs(2),
	RunE:  runSandboxWriteFile,
}

var sandboxReadFileCmd = &cobra.Command{
	Use:   "read-file <sandbox-id> <remote-path>",
	Short: "Read a file out of a sandbox (stdout, unless --to is given)",
	Args:  cobra.ExactArgs(2),
	RunE:  runSandboxReadFile,
}

var sandboxDeleteCmd = &cobra.Command{
	Use:   "delete <sandbox-id>",
	Short: "Delete a sandbox immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runSandboxDelete,
}

var sandboxPoolStatusCmd = &cobra.Command{
	Use:   "pool-status",
	Short: "Show the warm pool's per-template ready/warming counts",
	Long: `Show the warm pool's per-template ready/warming counts against the
operator-configured min_warm floor (#1488 Phase 4).

Prints "no warm pool configured" when the daemon has no pool set up
(the current production default) — this is expected, not an error.`,
	Args: cobra.NoArgs,
	RunE: runSandboxPoolStatus,
}

func init() {
	rootCmd.AddCommand(sandboxCmd)
	sandboxCmd.AddCommand(sandboxSpawnCmd, sandboxExecCmd, sandboxWriteFileCmd, sandboxReadFileCmd, sandboxDeleteCmd, sandboxPoolStatusCmd)

	sandboxSpawnCmd.Flags().Int32Var(&sandboxIdleTTLSeconds, "idle-ttl-seconds", 0, "idle TTL before the sweeper reaps an unclaimed sandbox (0 = daemon default)")
	sandboxSpawnCmd.Flags().BoolVar(&sandboxAllowCold, "allow-cold-start", false, "fall back to the cold path instead of RESOURCE_EXHAUSTED when the warm pool is empty (no effect in Phase 1 — there is no pool yet)")

	sandboxWriteFileCmd.Flags().StringVar(&sandboxWriteFrom, "from", "", "local file to write into the sandbox (required)")
	sandboxWriteFileCmd.Flags().StringVar(&sandboxWriteMode, "mode", "", "file mode, e.g. 0644 (default: daemon default)")

	sandboxReadFileCmd.Flags().StringVar(&sandboxReadTo, "to", "", "local file to write the content to (default: stdout)")
}

// newSandboxGRPCClient returns a gRPC client; sandbox verbs are
// server-side only (no local box to fall back to, unlike some container
// verbs).
func newSandboxGRPCClient() (*client.GRPCClient, error) {
	if serverAddr == "" {
		return nil, fmt.Errorf("--server is required")
	}
	return client.NewGRPCClient(serverAddr, certsDir, insecure)
}

func runSandboxSpawn(cmd *cobra.Command, args []string) error {
	c, err := newSandboxGRPCClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.SpawnSandbox(&pb.SpawnSandboxRequest{
		IdleTtlSeconds: sandboxIdleTTLSeconds,
		AllowColdStart: sandboxAllowCold,
	})
	if err != nil {
		return err
	}

	sb := resp.Sandbox
	fmt.Printf("✓ spawned %s\n", sb.SandboxId)
	fmt.Printf("  state:       %s\n", sb.State)
	fmt.Printf("  template:    %s\n", sb.Template)
	fmt.Printf("  served_from: %s\n", sb.ServedFrom)
