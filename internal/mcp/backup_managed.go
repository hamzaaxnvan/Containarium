package mcp

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/footprintai/containarium/internal/backupkey"
	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// managedKMSLoader builds the MCP host operator's own KMS client for
// managed=true (#2403), the same loader the CLI's --managed uses. Tests
// replace it with a fake.
var managedKMSLoader backupkey.KMSLoader = backupkey.LoadOperatorKMS

// managedSelectionProps are the restore_backup / verify_backup inputs that
// mirror the CLI's --managed, --latest, --user and --database.
func managedSelectionProps() map[string]interface{} {
	return map[string]interface{}{
		"managed": map[string]interface{}{
			"type":        "boolean",
			"description": "Unwrap the record's managed key (key_mode managed/both) on the MCP host with the operator's own cloud credentials (CONTAINARIUM_KMS_BACKEND=gcp, CONTAINARIUM_GCP_KMS_TOKEN=$(gcloud auth print-access-token)); the decrypt is audited by the KMS. A KMS permission error stops the call before any restore/verify reaches the host. Exclusive with age_identity_file. Mirrors --managed.",
