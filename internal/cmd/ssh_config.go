package cmd

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/hostport"
	"github.com/footprintai/containarium/internal/sshconfig"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/spf13/cobra"
)

var (
	sshConfigSentinel       string
	sshConfigJumpHost       string
	sshConfigPort           int
	sshConfigIdentity       string
	sshConfigUser           string
	sshConfigIncludeStopped bool
	sshConfigOutPath        string
	sshConfigForce          bool
)

var sshConfigCmd = &cobra.Command{
	Use:   "ssh-config",
	Short: "Generate a self-contained ssh_config for your containers",
	Long: `Generate an OpenSSH config file containing one Host block per container.

The file is self-contained — it does NOT modify your ~/.ssh/config. Add a
single line to ~/.ssh/config to wire it in once:

    Include ~/.containarium/ssh_config

After that, ` + "`ssh <container-name>`" + ` and ` + "`scp`" + ` work transparently.

Routing modes:

  - Local:               HostName=<container IP>; for LAN-reachable boxes.
  - Remote single VM:    ProxyJump through the API host's SSH port (22)
                         when the daemon does not advertise ssh_host.
                         Use --jump-host for an API tunnel or custom SSH port.
  - Via sentinel:        HostName=<sentinel>, User=<container-name>;
                         sshpiper on the sentinel routes by username.

Examples:

  # Print to stdout — review before writing
  containarium ssh-config show

  # Write the file (default: ~/.containarium/ssh_config)
  containarium ssh-config sync

  # Behind a sentinel
  containarium ssh-config sync --sentinel sentinel.example.com
