package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1685: authorization here is an explicit per-handler call
// (auth.RequireScope / RequireRole / RequireRoleOrScope / AuthorizeTenant /
// AuthorizeSecretTenant) at the top of each RPC implementation — there is no
// interceptor or method→scope table that gates by default. That design's
// one failure mode is a handler that omits the call: it is not denied, it is
// ungated for every caller, and nothing detects that.
//
// TestEveryRPCHasAuthGuard is the backstop. It has no access to a live
// server (constructing DualServer needs Incus, stores, and other
// dependencies this test intentionally avoids), so it works from two purely
// static sources instead:
//
//   - registeredServices below names every proto service dual_server.go
//     wires onto the authenticated grpc.Server, paired with the Go receiver
//     type that implements it (from dual_server.go's
//     pb.RegisterXServiceServer(grpcServer, <value>) call sites). Each
//     service's generated *_ServiceDesc gives the exact RPC method/stream
//     names actually registered — the true surface, not a hand-recount of
//     the .proto files.
//   - guardedMethods AST-scans every non-test .go file in this package for
//     a method whose body calls one of authGuardFuncs, keyed by
//     "ReceiverType.MethodName".
//
// A registered RPC with no guarded method and no authExemptions entry fails
// the test. This catches a forgotten guard on a NEW rpc automatically —
// registeredServices only needs an update when a whole new SERVICE is wired
// in dual_server.go, which is rare and visible in review.

// authGuardFuncs are the auth-package calls that gate an RPC. Presence of
// ANY of these anywhere in a handler's body counts as guarded — this proves
// a guard call EXISTS, not that it runs before every side effect; ordering
// and correctness of the check itself are what internal/auth's own tests
// and ordinary review cover.
var authGuardFuncs = map[string]bool{
	"RequireScope":          true,
	"RequireExplicitScope":  true, // #2415: RequireScope minus the unscoped fall-through
	"RequireRole":           true,
	"RequireRoleOrScope":    true,
	"AuthorizeTenant":       true,
	"AuthorizeSecretTenant": true,
}

// rpcSurface pairs one registered service's generated descriptor with the Go
// type (no "*") whose methods implement it.
type rpcSurface struct {
	desc     grpc.ServiceDesc
	receiver string
}

// registeredServices is every proto service dual_server.go registers onto
// the authenticated daemon grpc.Server. Two proto services are deliberately
// absent, not oversights:
//
//   - SpawnService: served on a private unix socket with its own bare
//     grpc.NewServer() and no interceptor chain at all
//     (internal/agentbox/spawn_listener.go) — a filesystem-permission
//     boundary, not a scope boundary, and never reachable through the
//     mTLS-gated daemon surface this test audits.
//   - EventService: not registered anywhere in dual_server.go as of this
//     writing. That may be a real gap rather than a deliberate omission;
//     tracked separately (this test only audits what IS wired, so an
//     unregistered service is out of its scope by construction).
//
// Update this list when dual_server.go registers a new SERVICE — a new RPC
// on an existing service needs no change here, since it's picked up from
// that service's ServiceDesc automatically.
var registeredServices = []rpcSurface{
	{pb.ContainerService_ServiceDesc, "ContainerServer"},
	{pb.ComposeAutostartService_ServiceDesc, "ComposeAutostartServer"},
	{pb.NetworkService_ServiceDesc, "NetworkServer"},
	{pb.RecipeService_ServiceDesc, "RecipeServer"},
	{pb.NetworkPolicyService_ServiceDesc, "NetworkPolicyServer"},
	{pb.AgentSkillService_ServiceDesc, "AgentSkillServer"},
	{pb.CrewService_ServiceDesc, "CrewServer"},
	{pb.BackupService_ServiceDesc, "BackupServer"},
	{pb.VolumeService_ServiceDesc, "VolumeServer"},
	{pb.SandboxService_ServiceDesc, "SandboxServer"},
	{pb.AnonymousBoxService_ServiceDesc, "AnonymousBoxServer"},
	{pb.ClusterService_ServiceDesc, "ClusterServer"},
	{pb.KmsService_ServiceDesc, "KmsServer"},
	{pb.TrafficService_ServiceDesc, "TrafficServer"},
	{pb.AppService_ServiceDesc, "AppServer"},
	{pb.SecurityService_ServiceDesc, "SecurityServer"},
	{pb.PentestService_ServiceDesc, "PentestServer"},
	{pb.ZapService_ServiceDesc, "ZapServer"},
	{pb.TokensService_ServiceDesc, "TokensServer"},
	{pb.ThreatDetectionService_ServiceDesc, "ThreatDetectionServer"},
	{pb.TrackerService_ServiceDesc, "ContainerServer"},
	// #1726 — every RPC here carries an explicit gateway:admin or gateway:mint
	// RequireScope in its own body; none is exempt.
	{pb.ModelGatewayService_ServiceDesc, "ModelGatewayServer"},
	// #2378 — Set/Delete RequireRole(admin); Get RequireRoleOrScope + AuthorizeTenant.
	{pb.CodingToolEgressPolicyService_ServiceDesc, "CodingToolEgressPolicyServer"},
	// #2185 — Set/Delete RequireScope(secrets:write), Get/Test RequireScope(secrets:read); all AuthorizeTenant via notifyUser.
	{pb.NotifyService_ServiceDesc, "NotifyServer"},
	// #2368 — Set is RequireRole(admin); Get is any authenticated subject
	// (the manual SubjectFromGRPCContext/Unauthenticated pattern).
	{pb.GuardrailPolicyService_ServiceDesc, "GuardrailPolicyServer"},
	// #2875 — every RPC is RequireRole(admin) (through requireRetentionAdmin).
	{pb.RetentionService_ServiceDesc, "RetentionServer"},
	{pb.AuditService_ServiceDesc, "AuditServer"},         // #2415
	{pb.ProofPackService_ServiceDesc, "ProofPackServer"}, // #3064 — RequireScope(audit:read)
	// #2516 — Authorize is RequireExplicitScope(ssh:authorize), stamped by
	// the gateway only on a verified sentinel signature.
	{pb.SSHAuthzService_ServiceDesc, "SSHAuthzServer"},
}

// authExemptions lists every registered RPC whose handler carries no auth
// guard call, and why. A registered RPC missing from BOTH the AST scan's
// guarded set and this map fails TestEveryRPCHasAuthGuard.
//
// Keep this narrow and reasoned — per #1685, "the exemption list states WHY
// for each entry, not just THAT." Adding an entry widens what an
// unauthenticated caller can reach; it must be a deliberate, reviewed
// choice, never a way to silence a red test.
//
// Every real finding this audit produced (#1716 ComposeAutostartService,
// #1717 ThreatDetectionService's bad-destination mutation, #1718's 9
// read-only RPCs) has now landed — merged to main as PR #1720 and PR #1721,
// both while this PR was in flight. TestEveryRPCHasAuthGuard itself caught
// each batch as stale (guarded-and-still-exempted) as soon as this branch
// merged with the new main each time, which is exactly the mechanism
// CodeRabbit asked for on this PR. RefreshToken is the only entry left, and
// it always was a design choice rather than a finding — see its own
// comment below.
var authExemptions = map[string]string{
	// #1685 itself: refresh is self-authenticating via the refresh_token
	// payload's own signature (validated by tokenManager.ValidateRefreshToken)
	// — requiring a bearer scope would be circular, since refresh exists
	// precisely because the caller's access token has already expired. Not a
	// gap; the design this coverage test's own model doesn't have a category
	// for ("guarded by a credential that isn't a scoped bearer token").
	"TokensService/RefreshToken": "self-authenticating via the refresh_token payload's own signature, not a bearer scope — see #1685",
}

func rpcKey(service, method string) string { return service + "/" + method }

// serviceShortName strips ServiceDesc.ServiceName's "containarium.v1."
// package prefix, leaving e.g. "ContainerService".
func serviceShortName(full string) string {
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

// funcInfo is one function or method's own (non-transitive) auth signals,
// plus the bare names of the local functions/methods it calls — the edges a
// transitive walk follows to find a guard applied through a helper (e.g.
// CreateContainerSnapshot's guard lives in the shared snapshotDatasetFor,
// not in CreateContainerSnapshot's own body).
type funcInfo struct {
	recv        string // "" for a free function
	name        string
	directGuard bool // calls auth.<one of authGuardFuncs> in ITS OWN body
	// manualGuardLinked is the GetMetrics/ListContainers-shaped pattern: an
	// `if !ok { ...codes.Unauthenticated/PermissionDenied... }` whose
	// condition negates the ok returned by auth.SubjectFromGRPCContext. Both
	// halves must be linked by that ONE if-statement — a function that
	// merely calls SubjectFromGRPCContext (for logging, say) and separately,
	// coincidentally, returns an unrelated PermissionDenied elsewhere does
	// NOT set this (tightened per CodeRabbit review on PR #1719: the
	// original "both signals anywhere in the same body" version couldn't
	// tell the two apart).
	manualGuardLinked bool
	calls             []string
}

// selfGuarded is true when a function's OWN body — not any callee's — is
// enough to call it guarded: either a direct canonical guard call, or the
// manual, control-flow-linked pattern described on manualGuardLinked.
func (f funcInfo) selfGuarded() bool {
	return f.directGuard || f.manualGuardLinked
}
