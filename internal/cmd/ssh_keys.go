package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	sshKeysServer   string
	sshKeysKeys     []string
	sshKeysKeyFiles []string
	sshKeysEmpty    bool
)

var sshKeysCmd = &cobra.Command{
	Use:   "ssh-keys",
	Short: "Manage the SSH keys authorized on a box",
}

var sshKeysSetCmd = &cobra.Command{
	Use:   "set <box>",
	Short: "Replace a box user's authorized SSH keys with exactly the given list",
	Long: `Make the box user's authorized SSH keys exactly the keys you pass.

Keys not in the list are removed and revoked; keys the platform manages
itself are always kept. Running the same command twice gives the same result.
Every key must be a single-line public key with no options (command=, from=):
one invalid key fails the whole call and changes nothing.

The daemon's key registry has one entry per key, owned by the first account
(box user) that registered it (a revoked entry is replaced by the next
registration); a box user also owns an entry its own
collaborator account (<owner>-container-<user>) registered on another box.
A dropped key is always removed from this box. It is revoked in the registry
when this box user owns that entry, or the entry has no owner, and no other
account still holds the same public key in its own authorized_keys (a
collaborator account is a separate account here). A key another account holds
is only removed from this box and its entry stays active; if this box user
owned the entry, it loses its owner, so the next account to add the key takes
it over and the last holder's own removal revokes it. When another account
owns the entry it stays active. Dropping a key here is therefore not a
guaranteed way to revoke a compromised key everywhere.

An empty list removes every key you manage and must be asked for with --empty.

Examples:
  containarium ssh-keys set my-box --key-file ~/.ssh/id_ed25519.pub --key-file ./ci.pub
  containarium ssh-keys set my-box --empty`,
	Args: cobra.ExactArgs(1),
	RunE: runSSHKeysSet,
}

func init() {
