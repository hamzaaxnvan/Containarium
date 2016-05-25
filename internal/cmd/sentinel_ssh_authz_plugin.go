//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshauthz"
	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	"github.com/spf13/cobra"
	"github.com/tg123/sshpiper/libplugin"
)

var (
	sentinelSSHAuthzSocket       string
	sentinelSSHAuthzPluginSocket string
	sentinelSSHAuthzGrace        time.Duration
	sentinelSSHAuthzTimeout      time.Duration
	sentinelSSHAuthzEnforce      bool
	sentinelSSHAuthzRecordsFile  string
	sentinelSSHAuthzConfigFile   string
)

var sentinelSSHAuthzPluginCmd = &cobra.Command{
	Use:   "ssh-authz-plugin",
	Short: "sshpiperd plugin: check live SSH authorization before ssh-session-plugin",
	Long: `Run as the first sshpiperd chained plugin. Every offered raw key is
checked against its routed backend through the sentinel manager's private
Unix socket; the manager signs the daemon request. Only publickey is offered
to the client; password, keyboard-interactive and none are refused here.
Positive cache TTL comes from the daemon, and outage grace never renews the
last successful authorization.

Audit-only (the default): every offer defers to the next plugin with the
decision as metadata. Certificates and unknown logins are not checked.
ssh-session-plugin writes the session records, and yaml's static
