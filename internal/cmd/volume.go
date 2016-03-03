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
	RunE:  runVolumeCreate,
}

var volumeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List shared volumes (and whether this backend supports them)",
	Args:  cobra.NoArgs,
	RunE:  runVolumeList,
}

var volumeDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a shared volume (refuses while attached unless --force)",
	Args:  cobra.ExactArgs(1),
	RunE:  runVolumeDelete,
}

var volumeAttachCmd = &cobra.Command{
	Use:   "attach <volume> <container>",
	Short: "Mount a shared volume into a container (read-write by default)",
	Args:  cobra.ExactArgs(2),
	RunE:  runVolumeAttach,
}

var volumeDetachCmd = &cobra.Command{
	Use:   "detach <volume> <container>",
	Short: "Unmount a shared volume from a container",
	Args:  cobra.ExactArgs(2),
	RunE:  runVolumeDetach,
}

func init() {
	rootCmd.AddCommand(volumeCmd)
	volumeCmd.AddCommand(volumeCreateCmd, volumeListCmd, volumeDeleteCmd, volumeAttachCmd, volumeDetachCmd)

	volumeCreateCmd.Flags().StringVar(&volumeSize, "size", "", "volume quota, e.g. 50GB or 1TB (required)")
	volumeCreateCmd.Flags().StringVar(&volumePool, "pool", "", "CephFS pool (default: the backend's detected pool)")
	volumeListCmd.Flags().StringVar(&volumePool, "pool", "", "CephFS pool to list (default: detected)")
	volumeDeleteCmd.Flags().StringVar(&volumePool, "pool", "", "CephFS pool (default: detected)")
	volumeDeleteCmd.Flags().BoolVar(&volumeForce, "force", false, "delete even if still attached")
	volumeAttachCmd.Flags().StringVar(&volumePool, "pool", "", "CephFS pool (default: detected)")
	volumeAttachCmd.Flags().StringVar(&volumePath, "path", "", "mount path inside the container (required)")
	volumeAttachCmd.Flags().BoolVar(&volumeReadOnly, "read-only", false, "mount read-only")
}

// parseSizeBytes parses a human size like "50GB", "1TiB", "500M", or a
// bare byte count into bytes. KB/MB/GB/TB are decimal (1000-based);
