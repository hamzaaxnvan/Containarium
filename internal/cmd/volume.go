package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Shared, multi-writer CephFS data volumes (#384). Capability-gated on the
// daemon: create/attach are rejected unless the backend has a cephfs pool.
// gRPC-only for now (mirrors other server-side verbs).

var volumeCmd = &cobra.Command{
	Use:   "volume",
	Short: "Manage shared, multi-writer (CephFS) data volumes",
	Long: `Create, list, attach, and detach CephFS custom volumes that can be
mounted read-write into multiple containers at once.

Only a CephFS-backed backend can serve these (CephFS coordinates concurrent
writers; a block device on ZFS/ext4 cannot). On a single-node ZFS host the
daemon reports the capability as unsupported and rejects create/attach.

  containarium volume list --server <host>
  containarium volume create dataset --size 50GB --server <host>
  containarium volume attach dataset alice --path /mnt/shared --server <host>
  containarium volume detach dataset alice --server <host>
  containarium volume delete dataset --server <host>`,
}

var (
	volumeSize     string
	volumePool     string
	volumeForce    bool
	volumePath     string
	volumeReadOnly bool
)

var volumeCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a shared CephFS volume with a quota",
	Args:  cobra.ExactArgs(1),
