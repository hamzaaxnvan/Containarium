package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	newCPU           string
	newMemory        string
	newDisk          string
	newMemoryRequest string
	newCPURequest    string
)

var resizeCmd = &cobra.Command{
	Use:   "resize <username>",
	Short: "Resize container resources (CPU, memory, disk)",
	Long: `Dynamically adjust container resources without downtime.

All changes take effect immediately without restarting the container.

Examples:
  # Resize CPU only
  containarium resize alice --cpu 4

  # Resize memory only
  containarium resize alice --memory 8GB

  # Resize disk only
  containarium resize alice --disk 100GB

  # Resize all at once
  containarium resize alice --cpu 4 --memory 8GB --disk 100GB

  # Adjust only the K8s scheduler reservation, leaving the ceiling alone
  containarium resize alice --cpu-request 1

  # Remote mode
  containarium resize alice --cpu 4 --memory 8GB \
      --server 35.229.246.67:50051 \
      --certs-dir ~/.config/containarium/certs

Resource Limits:
  CPU:    Number of cores (e.g., 2, 4, 8) or range (2-4)
  Memory: Size with unit (e.g., 4GB, 8192MB, 16GiB)
  Disk:   Size with unit (e.g., 50GB, 100GB, 500GB)
