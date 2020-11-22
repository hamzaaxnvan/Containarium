//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/sshkey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium ssh-authz-keys` is the jump host's sshd AuthorizedKeysCommand
// (#2527, part of the SSH live authorization design in #2515). It covers
// boxes reachable without the sentinel: a direct SSH login to the LXC jump
// host asks the local daemon's SSHAuthzService.Authorize, the same decision
// the sentinel plugin gets.
//
// sshd runs it as
//
//	containarium ssh-authz-keys %u %f %t %k
//
// and accepts the key only if it is printed back as an authorized_keys line.

const (
	// DefaultSSHAuthzKeysTokenFile holds the dedicated ssh:authorize JWT the
	// startup script mints at install time, readable only by
	// AuthorizedKeysCommandUser.
	DefaultSSHAuthzKeysTokenFile = "/etc/containarium-authz/ssh-authz-keys.token" // #nosec G101 -- a file PATH, not a credential
	// DefaultSSHAuthzKeysDaemonURL is the local daemon's REST gateway.
	DefaultSSHAuthzKeysDaemonURL = "http://127.0.0.1:8080"
	// defaultSSHAuthzKeysTimeout bounds the daemon call: sshd waits for the
	// command inside LoginGraceTime.
	defaultSSHAuthzKeysTimeout = 5 * time.Second
)

var (
	sshAuthzKeysTokenFile string
	sshAuthzKeysDaemonURL string
	sshAuthzKeysTimeout   time.Duration
	sshAuthzKeysEnforce   bool
)

var sshAuthzKeysCmd = &cobra.Command{
	Use:   "ssh-authz-keys <login> <fingerprint> <key-type> <key>",
	Short: "sshd AuthorizedKeysCommand: print the key only if the daemon authorizes it",
	Long: `Asks the local daemon whether <key> may open a session as <login>
(SSHAuthzService.Authorize) and prints it as an authorized_keys line on ALLOW.
On DENY it prints nothing, so sshd does not accept the key from this command.

Meant to be run by sshd, not by hand. The jump host's sshd drop-in runs it as:

  AuthorizedKeysCommand /usr/local/bin/containariumd ssh-authz-keys --token-file <file> %u %f %t %k

<fingerprint> must match <key> (sshd's SHA256 form); a mismatch is refused.

When the daemon cannot be asked (unreachable, token refused, timeout) no
decision is made: the command exits 0 in audit-only mode and 1 with --enforce.
Either way it prints nothing.

The token file holds a JWT carrying the ssh:authorize scope; the daemon must
run with --ssh-authz-keys-command to accept it.`,
	Args: cobra.ExactArgs(4),
	// sshd runs this as an unprivileged account with no home or credentials
	// file; skip the root command's server/token resolution.
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	SilenceUsage:      true,
	SilenceErrors:     true,
	RunE: func(cmd *cobra.Command, args []string) error {
		code := runSSHAuthzKeys(cmd.Context(), sshAuthzKeysOptions{
			Login:       args[0],
			Fingerprint: args[1],
			KeyType:     args[2],
			Key:         args[3],
			Enforce:     sshAuthzKeysEnforce,
			Timeout:     sshAuthzKeysTimeout,
			Authorize:   httpSSHAuthorizer(sshAuthzKeysDaemonURL, sshAuthzKeysTokenFile),
		}, os.Stdout, os.Stderr)
		if code != 0 {
			os.Exit(code)
		}
		return nil
	},
