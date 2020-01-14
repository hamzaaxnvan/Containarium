package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/gatewayprovider"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// The engine + credential wiring for `containarium code` (#1727).
//
// This file is the cmd-layer half of the seam: internal/coderun/engine decides
// WHAT to run (pure, shell-rendering, table-tested), and this decides WHEN —
// which preflight to run, when to mint, where to write. The split is what keeps
// the engine table unit-testable without a daemon.
//
// It consumes #1726's ModelGatewayService and does not modify it.

// codeVerifyTokenTTL is the lifetime of the token `code install` mints purely to
// verify the box can reach a model. Short on purpose: it proves reachability
// once and then stops being useful. Every real run mints its own.
const codeVerifyTokenTTL = 5 * time.Minute

// codeRunTokenTTLDefault is the default lifetime of a per-run gateway token.
//
// A run is the natural lease (design doc's token-lifetime table): a leaked token
// is then one box, one model, one run, and at most a day — and revocable by its
// jti before that. Capped server-side regardless of what is asked for here.
const codeRunTokenTTLDefault = 24 * time.Hour

// codeInstallPlan is everything the flags resolved to, validated together.
// Built once so no later step re-derives a choice and disagrees.
type codeInstallPlan struct {
	engine     engine.Engine
	credential engine.CredentialSource
	config     engine.CodeConfig
	// provider is the gateway provider enum, unset for a secret credential.
	provider pb.GatewayProvider
	// baseURLOverride is --provider-base-url, verbatim.
	baseURLOverride string
	// version is the engine version pin (--claude-code-version / --pi-version).
	version string
}

// resolveCodeInstallPlan validates the engine/credential flag combination.
//
// Every rejection here names the fix. The combinations are cheap to get wrong
// (--credential gateway without --provider, --engine pi without --model) and
// expensive to discover on a box.
func resolveCodeInstallPlan(box string) (*codeInstallPlan, error) {
	name, err := engine.ParseName(codeEngine)
	if err != nil {
		return nil, err
	}
	kind, err := engine.ParseCredentialKind(codeCredential)
	if err != nil {
		return nil, err
	}

	// #2273: codex's gateway path depends on the model gateway's provider
	// generalization (#1369/#1374 on Containarium-cloud), which this change
	// does not assume has landed. Rejecting here — before any preflight —
	// means a user who tries it gets the fix named instead of an untested
	// path reaching a box. Tracked as a follow-up, not a redesign: the
	// gateway-shaped methods already exist on codexEngine (codex.go).
	if name == engine.NameCodex && kind == engine.KindGateway {
		return nil, fmt.Errorf(
			"--engine codex --credential gateway is not supported yet (tracked as a follow-up to #2273) — " +
				"use --credential secret --secret-name CODEX_API_KEY (or OPENAI_API_KEY)")
	}

	plan := &codeInstallPlan{baseURLOverride: strings.TrimSpace(codeProviderBaseURL)}

	switch kind {
	case engine.KindGateway:
		if strings.TrimSpace(codeProvider) == "" {
			return nil, fmt.Errorf(
				"--credential gateway needs --provider (one of: %s)", strings.Join(gatewayprovider.Names(), ", "))
		}
		provider, err := gatewayprovider.FromName(codeProvider)
		if err != nil {
			return nil, err
		}
		if s := strings.TrimSpace(codeSecretName); s != "" {
			return nil, fmt.Errorf("--secret-name is for --credential secret; --credential gateway mints a token instead")
		}
