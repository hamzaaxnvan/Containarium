package cmd

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/internal/backupkey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// managedKMSLoader builds the operator's own KMS client for --managed
// (#2403). Tests replace it with a fake.
var managedKMSLoader backupkey.KMSLoader = backupkey.LoadOperatorKMS

// managedHelp is the --managed paragraph shared by restore and verify.
const managedHelp = `A managed backup (created with --key-mode managed or both) carries its
own key, wrapped by the daemon's KMS key, which the daemon itself cannot
unwrap. With --managed this command fetches the record, unwraps that key
in THIS process using your own cloud credentials, and sends the identity
over the same TLS-only path as --age-identity-file. The decrypt is
audited by the KMS under your principal; a principal without the
decrypter role gets a permission error before any restore or verify call
reaches the host. Nothing is written to disk. On an operator machine:

  export CONTAINARIUM_KMS_BACKEND=gcp
  export CONTAINARIUM_GCP_KMS_TOKEN=$(gcloud auth print-access-token)`

// backupSelection is how a restore or verify names its record: a
// positional id, or --latest with --user and --database.
type backupSelection struct {
	args            []string
	latest          bool
	user, database  string
	managed         bool
	ageIdentityFile string
}

// validate refuses every combination that would leave the command
// guessing, before anything is dialed or unwrapped.
