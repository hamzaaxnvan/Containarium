//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium proof-pack` — evidence that a repository's tests ran,
// recorded by the platform rather than claimed by the coding agent
// (docs/PROOF-PACK.md, #2366). generate runs on the host that owns the box
// and execs into it through the local Incus backend, the same
// proofpack.Generate the daemon calls; it is a server-side command, so the
// client binary does not carry it.
var proofPackCmd = &cobra.Command{
	Use:   "proof-pack",
	Short: "Generate a Proof Pack: the repo's tests run in a box, with outcomes and logs",
}

var (
	proofPackDir         string
	proofPackOut         string
	proofPackStepTimeout time.Duration
)

// proofPackExecer is the backend generate execs through; tests replace it.
var proofPackExecer = func() (proofpack.Execer, error) { return incus.New() }

var proofPackGenerateCmd = &cobra.Command{
	Use:   "generate <container>",
	Short: "Run the repo's declared test commands in a box and write the Proof Pack",
	Long: `Run the repository's test command set inside <container>, from --dir,
and write the resulting Proof Pack as JSON to --out (stdout by default).

The commands are the "test:" key of the repository's
.github/containarium.yml, either one command or a list of steps:

  test: make check

  test:
    - name: unit
      kind: unit            # unit | integration | e2e | static_analysis | security_scan | test
      run: go test ./...
    - name: lint
      kind: static_analysis
      run: golangci-lint run

With no "test:" key the fallback is "make test" when the repository has a
Makefile; otherwise nothing runs and the pack's status is NO_COMMANDS.

Each step runs as root in the box, under --step-timeout, with its combined
stdout and stderr kept up to the last 256 KiB. The pack records each
step's declared name, kind, exit status, duration, log and the log's
SHA-256. It never records the command lines or the environment.

Exit status: 0 when the pack's status is PASSED; 1 when it is FAILED or
NO_COMMANDS (the pack is written either way); non-zero without a pack
when the commands could not be run at all.

Example:
