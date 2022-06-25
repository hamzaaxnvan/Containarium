package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	moveTargetBackend         string
	moveMaxIterations         int32
	moveDeltaThresholdSeconds int32
	moveStateful              bool
)

var moveCmd = &cobra.Command{
	Use:   "move <username>",
	Short: "Migrate a container to a peer daemon",
	Long: `Pre-copy snapshot-based migration of a container to a peer daemon.

