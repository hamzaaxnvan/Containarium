// Package backupkey holds the operator-side steps of a managed-key restore
// or verify (#2403), shared by `containarium backup restore|verify` and the
// MCP restore_backup / verify_backup tools so both run the same code:
// picking the newest record, and unwrapping a record's per-backup age
// identity under the caller's own KMS credentials. The daemon never
// unwraps; this runs in the operator's process, so KMS IAM decides who may
// decrypt and the KMS audit log records who did.
package backupkey

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// KMSLoader builds the KMS client that can unwrap a key wrapped under
// kekID. It is the seam tests replace with a fake.
type KMSLoader func(kekID string) (secrets.KMSClient, error)

// operatorEnvHint is the setup an operator machine needs, named in every
// error that means "your KMS environment is not set up".
const operatorEnvHint = "set CONTAINARIUM_KMS_BACKEND=gcp and CONTAINARIUM_GCP_KMS_TOKEN=$(gcloud auth print-access-token) " +
