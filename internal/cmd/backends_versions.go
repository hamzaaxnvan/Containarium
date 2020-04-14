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
	UpdateAvailable bool   `json:"updateAvailable"`
	TargetVersion   string `json:"targetVersion"` // sentinel-served version (#2171)
}

// backendVersionRow is the rendered per-backend version line.
type backendVersionRow struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
	Status  string `json:"status"` // current | behind | dev | unknown
}

type backendsVersionsOutput struct {
	LatestRelease string `json:"latestRelease"`
	// SentinelVersion is what `backends upgrade` would install (the
	// sentinel-served binary); empty when unknown. #2171.
	SentinelVersion string              `json:"sentinelVersion"`
	Backends        []backendVersionRow `json:"backends"`
}

// backendVersionStatus classifies a backend's version against the latest
// published release. "behind" when the latest is a newer semver; "current"
// when equal or ahead; "dev" for an unversioned dev build; "unknown" when
// either side is missing or unparseable.
func backendVersionStatus(current, latest string) string {
	if current == "" {
		return "unknown"
	}
	if current == "dev" {
		return "dev"
	}
	if latest == "" {
		return "unknown"
	}
	if releasecheck.UpdateAvailable(current, latest) {
		return "behind"
	}
	return "current"
}

func runBackendsVersions(cmd *cobra.Command, args []string) error {
	// Backend version / release checks are operator-owned on the hosted
