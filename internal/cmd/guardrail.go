package cmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium guardrail` — run a detection engine over a directory of
// text, redact what it flags, re-scan the result as the gate, and sign an
// attestation a consumer verifies before using the data
// (docs/architecture/guardrail.md). This is the data owner's side of a trust
// boundary: scanning, redaction and signing run on the machine the CLI runs
// on, and no dataset content ever leaves it. Since #2368 that is no longer
// "nothing talks to the daemon": `apply` and `verify` READ the server-side
// guardrail policy when a server is named explicitly with --server or
// CONTAINARIUM_SERVER (guardrail_server.go states the rule and exactly what
// is sent, which is only that read), and `guardrail policy`
// (guardrail_policy.go) reads or sets it. The engine is a separate process reached over
// gRPC (--engine); with no --engine the in-tree reference rules engine
// runs, which is a handful of regular expressions for proving the flow,
// not a detector anyone should ship data on.
var guardrailCmd = &cobra.Command{
	Use:   "guardrail",
	Short: "Scan, redact, gate and attest a dataset before it leaves your side",
}

var (
	guardrailEngineAddr   string
	guardrailKinds        []string
	guardrailJSON         bool
	guardrailOut          string
	guardrailPolicyFile   string
	guardrailVaultFile    string
	guardrailRedactKey    string
	guardrailSignKey      string
	guardrailAttestFile   string
	guardrailPublicKey    string
	guardrailRequireKinds []string
	guardrailKeygenPrefix string
)

var guardrailScanCmd = &cobra.Command{
	Use:   "scan <dir>",
	Short: "Report what the engine finds in a directory, without changing anything",
	Long: `Scan every regular file under <dir> and print findings per kind and
type. Nothing is written. --json prints each finding with its unit and
code-point offsets — never the flagged text itself, so the output is safe
to keep in a log.

Examples:
  containarium guardrail scan ./export
  containarium guardrail scan ./export --engine 127.0.0.1:9090 --kind pii --json`,
	Args: cobra.ExactArgs(1),
	RunE: runGuardrailScan,
}

var guardrailApplyCmd = &cobra.Command{
	Use:   "apply <dir>",
	Short: "Redact findings into --out, re-scan as the gate, and write an attestation",
	Long: `Scan <dir>, replace every finding with a stable placeholder token,
write the result under --out, scan --out again, and decide: PASS when the
re-scan is within policy and nothing was unscannable, FAIL otherwise (exit
status 1). The attestation is written either way so a FAIL is on record.

The vault (token -> original) and the redaction key are written beside
--out, never inside it; they are the only way back and stay with you.
With --sign-key the attestation is signed so a consumer can verify it with
the matching .pub (see 'guardrail keygen' and 'guardrail verify').

Policy: apply contacts a server only when one is named explicitly, with
--server or CONTAINARIUM_SERVER. A login's default server does not count;
logging in does not make apply contact the platform. With a named server
that holds a guardrail policy, apply runs under the server's policy, and
--policy is refused unless it is the same policy (same hash). The only
request is a read of the policy, carrying your credentials; no dataset
content is sent. If the named server cannot be reached or answers with an
error, apply fails (non-zero exit); it never falls back silently. With no
named server, or a named server that has no policy configured, apply runs
under --policy (or the default) and says the result is NOT server-attested.

Examples:
  containarium guardrail keygen --out ./tenant
  containarium guardrail apply ./export --out ./export-clean --sign-key ./tenant.key
  containarium guardrail apply ./export --out ./clean --engine 127.0.0.1:9090 --policy ./policy.json`,
	Args: cobra.ExactArgs(1),
	RunE: runGuardrailApply,
}

var guardrailVerifyCmd = &cobra.Command{
	Use:   "verify <dir>",
	Short: "Check a directory against its attestation before using the data",
	Long: `Verify the attestation's signature, recompute the digest of <dir> and
require it to match, and require the verdict to be PASS. Exit status 0 only
when all hold. This is what a training job or a CI step runs first.
