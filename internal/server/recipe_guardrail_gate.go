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
// <staging root>/<subject>/<staging_ref>, where subject is the AUTHENTICATED
// subject, never the deploy name (#2414). An admin may deploy under any
// name, so a name-derived scope would let an admin-token proxy that
// forwards a user-chosen name read any tenant's staged data; an attestation
// proves data is clean, not that the caller may have it. For a non-admin
// the two are the same (AuthorizeTenant already requires name == subject).
// The ref is joined WITHOUT cleaning, so a "../" in it stays visible to the
// staging area's clean-path check and is refused there.
func guardrailStagingRef(subject, ref string) (string, error) {
	if subject == "" || strings.ContainsAny(subject, `/\`) || subject == "." || subject == ".." {
		return "", fmt.Errorf("%w: caller subject %q cannot scope a staging directory", guardrailstage.ErrBadRef, subject)
	}
	return subject + "/" + ref, nil
}

func gateRefusal(format string, args ...any) error {
	return status.Errorf(codes.FailedPrecondition, "guardrail gate: "+format, args...)
}

// checkGuardrailGate runs steps 1-4. On success it returns the verified
// snapshot, which the caller delivers and then removes.
func (s *RecipeServer) checkGuardrailGate(ctx context.Context, recipe *pb.Recipe, req *pb.DeployRecipeRequest) (*guardrailstage.Snapshot, error) {
	// Rate limit (#2404) first: a throttled caller starts no policy read, no
	// staging access and no snapshot.
	if err := checkGuardrailRate(ctx, s.guardrailDeployLimit); err != nil {
		return nil, err
	}
	if req.GetAsync() {
		return nil, gateRefusal("recipe %q is guardrail-gated and cannot be deployed async; deploy synchronously", recipe.GetId())
	}
	if s.guardrailPolicy == nil {
		return nil, gateRefusal("no server guardrail policy: this daemon has no policy store wired")
	}
	policy, err := s.guardrailPolicy.Get(ctx)
	if errors.Is(err, guardrailpolicy.ErrNotConfigured) {
		return nil, gateRefusal("no server guardrail policy is configured; an admin sets one with 'containarium guardrail policy set'")
	}
	if err != nil {
		// Never "no policy": the policy is unknown, so the deploy is refused.
		log.Printf("[recipe] guardrail gate: read policy: %v", err)
		return nil, status.Error(codes.Unavailable, "guardrail gate: cannot read the server guardrail policy; refusing the gated deploy")
	}
	if len(policy.GetTrustedSigners()) == 0 {
		return nil, gateRefusal("the server guardrail policy has no trusted signer")
	}
	in := req.GetGuardrailInput()
	if in == nil || in.GetAttestation() == nil || in.GetStagingRef() == "" {
		return nil, gateRefusal("recipe %q is guardrail-gated: guardrail_input with staging_ref and attestation is required", recipe.GetId())
	}
	// Errors below that are not one of the typed refusals may carry
	// daemon-side paths: they are logged, and the caller gets a generic
	// message (guardrailstage's error contract).
	area, err := guardrailstage.New(s.guardrailStagingRoot)
	if errors.Is(err, guardrailstage.ErrNoStagingRoot) {
		return nil, gateRefusal("this daemon has no guardrail staging root configured")
