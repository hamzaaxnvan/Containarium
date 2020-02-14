package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// These are the REMOTE (API) KMS admin commands — they call the
// daemon's KmsService over gRPC/HTTP and require an admin token with
// the kms:admin scope. They mirror the surface the dashboard consumes.
//
// The host-only counterparts under `secrets migrate-to-envelope` /
// `secrets envelope-coverage` (secrets_migrate.go) open the Postgres
// store directly and run ON the daemon host with master-key access —
// use those for on-box maintenance without a token.

var (
	kmsMigrateDryRun    bool
	kmsMigrateBatchSize int64
	kmsMigrateMaxRows   int64
)

var kmsCmd = &cobra.Command{
	Use:   "kms",
	Short: "Inspect KMS envelope-encryption status and trigger migration (remote)",
	Long: `Administer the tenant-secrets envelope-encryption layer over the API.

Requires an admin token carrying the kms:admin scope. Backend SELECTION
stays an operator/systemd concern (CONTAINARIUM_KMS_BACKEND + per-backend
env); these commands only read status / coverage and trigger migration.`,
}

var kmsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active KMS backend and envelope-retirement state",
	RunE:  runKMSStatus,
}

var kmsCoverageCmd = &cobra.Command{
	Use:   "envelope-coverage",
	Short: "Count stored secrets by encryption mode (legacy vs envelope)",
	RunE:  runKMSCoverage,
