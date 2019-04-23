//go:build !windows

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/egressproxy"
	"github.com/footprintai/containarium/internal/hostport"
	"github.com/spf13/cobra"
)

var (
	evcServer    string
	evcSSH       string
	evcIdentity  string
	evcSocksPort int
	evcSSHFlags  []string
)

var egressViaClientCmd = &cobra.Command{
	Use:   "egress-via-client <box>",
	Short: "Route a box's egress through THIS machine (#808)",
	Long: `Route a box's outbound traffic through this machine — so the box (e.g. a
headless browser) egresses with your IP/network. Works even when the box can't
reach you (NAT'd laptop): the path rides the SSH you make to the box, and the
daemon bridges it into the box's network namespace.

What it does, as one command:
  1. starts a local SOCKS5 proxy on this machine (egresses via you),
  2. opens an SSH reverse-forward exposing that SOCKS on the box's host loopback,
  3. asks the daemon to bridge it into the box (source-restricted to the box),
  4. prints the in-box SOCKS address; Ctrl-C tears it all down.

Example:
  containarium egress-via-client cld-abcd1234 \
    --server <daemon-host:50051> \
    --ssh cld-abcd1234@asia-east1.containarium.dev -i ~/.containarium/keys/cld-abcd1234

  # then, inside the box: point apps at the printed socks5://<addr>
  # (Chrome: --proxy-server=socks5://<addr>)`,
	Args: cobra.ExactArgs(1),
	RunE: runEgressViaClient,
}

func init() {
	rootCmd.AddCommand(egressViaClientCmd)
	egressViaClientCmd.Flags().StringVar(&evcServer, "server", "", "daemon gRPC address for the control call (required)")
	egressViaClientCmd.Flags().StringVar(&evcSSH, "ssh", "", "the box's SSH target user@host (required), e.g. cld-abcd1234@asia-east1.containarium.dev")
	egressViaClientCmd.Flags().StringVarP(&evcIdentity, "identity", "i", "", "SSH private key for the box")
	egressViaClientCmd.Flags().IntVar(&evcSocksPort, "socks-port", 0, "local SOCKS port (0 = pick a free one)")
	egressViaClientCmd.Flags().StringArrayVar(&evcSSHFlags, "ssh-flag", nil, "extra flag passed to ssh (repeatable)")
}

var allocatedPortRE = regexp.MustCompile(`Allocated port (\d+) for remote forward`)

func runEgressViaClient(cmd *cobra.Command, args []string) error {
	box := args[0]
	if evcServer == "" || evcSSH == "" {
		return fmt.Errorf("--server and --ssh are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Local SOCKS5 (egresses via this machine).
	socksAddr, err := egressproxy.ServeSOCKS5(ctx, fmt.Sprintf("127.0.0.1:%d", evcSocksPort), cmd.Printf)
	if err != nil {
		return err
	}
	_, socksPort := hostport.Split(socksAddr, 0)
	cmd.Printf("local SOCKS5 on %s (egresses via this machine)\n", socksAddr)

	// 2. ssh -R 127.0.0.1:0:127.0.0.1:<socksPort> — dynamic host port; parse the
	//    one ssh allocates so there's no collision on the box's host.
	sshArgs := []string{
		"-N",
		"-R", fmt.Sprintf("127.0.0.1:0:127.0.0.1:%d", socksPort),
		"-o", "IdentitiesOnly=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ServerAliveInterval=15",
	}
	if evcIdentity != "" {
		sshArgs = append(sshArgs, "-i", evcIdentity)
	}
	sshArgs = append(sshArgs, evcSSHFlags...)
	sshArgs = append(sshArgs, evcSSH)

	// #nosec G204 -- args are operator-provided flags for their own ssh session
	ssh := exec.CommandContext(ctx, "ssh", sshArgs...)
	stderr, err := ssh.StderrPipe()
	if err != nil {
		return err
	}
	if err := ssh.Start(); err != nil {
		return fmt.Errorf("start ssh: %w", err)
	}
	defer func() { _ = ssh.Process.Kill() }()

	hostPort, err := readAllocatedPort(stderr, 20*time.Second)
