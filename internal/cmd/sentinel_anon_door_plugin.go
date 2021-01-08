//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tg123/sshpiper/libplugin"

	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/footprintai/containarium/internal/sentinel/anondoor"
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	sentinelAnonDoorDaemonURL      string
	sentinelAnonDoorUpstreamKey    string
	sentinelAnonDoorTimeout        time.Duration
	sentinelAnonDoorBanner         string
	sentinelAnonDoorUpstreamSocket string
)

var sentinelAnonDoorPluginCmd = &cobra.Command{
	Use:   "anon-door-plugin",
	Short: "sshpiperd plugin: the `ssh new.<domain>` door — any SSH key gets its own anonymous VM (#2198)",
	Long: `Run as an sshpiperd chained plugin in the door's OWN sshpiperd instance
(sshpiper-anon.service), never on the tenant listener and never by hand.

On public-key auth it asks the anon-pool daemon's AnonymousBoxService for the
box that belongs to the key's fingerprint (POST /v1/anon/boxes:ensure, signed
with the sentinel's existing request signature from
CONTAINARIUM_SENTINEL_AUTH_SECRET / CONTAINARIUM_SENTINEL_SIGNING_KEY — the
daemon's gateway maps that signature to the anon:door scope) and returns an
upstream pipe to it using /etc/sshpiper/upstream_key, exactly like a tenant
pipe. Any non-OK answer rejects the auth; password and keyboard-interactive
are refused outright.

Wire it after the audit plugin, e.g.:

