package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/releasecheck"
	"github.com/spf13/cobra"
)

var backendsVersionsFormat string

var backendsVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Show the daemon version of every backend vs the latest release",
	Long: `Show a cluster-wide version overview: the daemon version running on the
local daemon and each tunnel peer, the latest published GitHub release, and a
per-backend status (current / behind) so drift is visible at a glance.

Composes the existing /v1/backends (per-backend version) and /v1/releases/latest
(cached GitHub lookup) endpoints — both HTTP-only and admin-authenticated, so
this command requires --server pointing at the daemon's HTTP address. See #354.`,
	Aliases: []string{"version", "ver"},
	RunE:    runBackendsVersions,
}

func init() {
	backendsCmd.AddCommand(backendsVersionsCmd)
	backendsVersionsCmd.Flags().StringVarP(&backendsVersionsFormat, "format", "f", "table",
		"Output format: table, json")
}

// latestReleaseResponse mirrors the /v1/releases/latest (GetLatestRelease)
// response shape (camelCase from grpc-gateway). Kept local so a server-side
// schema change surfaces as a decode failure here, not a silent field-drop.
type latestReleaseResponse struct {
	LatestRelease   string `json:"latestRelease"`
	CurrentVersion  string `json:"currentVersion"`
