package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"strings"
	"syscall"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	"github.com/footprintai/containarium/internal/guardrailstage"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The DeployRecipe guardrail gate (#2368;
// docs/architecture/guardrail-inbound-and-server-policy.md, "The gate").
// For a recipe with guardrail_gate set, deploy runs, in this order:
//
//  1. refuse async, before anything is created;
//  2. refuse when there is no server policy or no trusted signer (a policy
//     READ ERROR is its own refusal, never "no policy");
//  3. refuse when guardrail_input is absent: the recipe, not the caller,
//     decides whether a gate applies;
//  4. refuse a bad attestation before copying anything
//     (guardrailpolicy.VerifyClaims: every check except the digest), then
//     snapshot the staged dataset and verify the snapshot in process
//     (guardrailpolicy.VerifyAttestation: trusted signer, policy hash,
//     PASS, kinds, digest). Any failure: FAILED_PRECONDITION, no box;
//  5. (in deploy) create the box and copy the snapshot, the verified
//     bytes and nothing else, to dataset_path;
//  6. (in deploy) run post_start.

// SetGuardrailGate wires the gate: the server policy provider, the daemon's
// guardrail staging root, and where verification snapshots are made.
// Startup only, after the policy store's startup swap. "" root leaves gated
// recipes refused. "" snapshotDir is the OS temp dir; a snapshotDir must be
// daemon-private and outside the staging root, or every gated deploy is
// refused (the stager's ErrBadParent rule, checked on every snapshot).
func (s *RecipeServer) SetGuardrailGate(provider guardrailpolicy.PolicyProvider, stagingRoot, snapshotDir string) {
	s.guardrailPolicy = provider
	s.guardrailStagingRoot = stagingRoot
	s.guardrailSnapshotParent = snapshotDir
}

// guardrailStagingRef scopes a request's staging_ref to the caller:
