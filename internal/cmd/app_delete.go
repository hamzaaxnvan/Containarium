package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	deleteUsername   string
	deleteForce      bool
	deleteRemoveData bool
)

var appDeleteCmd = &cobra.Command{
	Use:   "delete <app-name>",
	Short: "Delete an application",
	Long: `Delete a deployed application.

This will:
  - Stop and remove the Docker container
  - Remove the Docker image
  - Remove proxy configuration
  - Optionally remove source and data files

Examples:
