package mcp

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/credentials"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// API is the contract the MCP tool handlers consume. It has two
// implementations behind a single factory (newBackend):
//
//   - *Client       — the OSS daemon backend: the full surface.
//   - cloudClient   — the hosted control plane: host-LEVEL operations
//     (system info, release/upgrade, per-box debug) have no tenant-safe
//     meaning, so they return a clear "not available here" instead of a
//     round-trip the cloud could only reject.
//
// Handlers take an API and never branch on the target themselves — the
// divergence lives in the implementation, not in `if isCloud {…}` guards
// scattered across handlers. newBackend classifies the target from the
// credential (a `ctnr_` token ⇒ cloud) and hands back the right one.
//
// The method set is exactly *Client's exported surface; the compile-time
// assertions below pin both implementations to it.
type API interface {
	// Containers + lifecycle.
	CreateContainer(req CreateContainerRequest) (*CreateContainerResponse, error)
	ListContainers() (*ListContainersResponse, error)
	GetContainer(username string) (*GetContainerResponse, error)
	DeleteContainer(username string, force bool) (*DeleteContainerResponse, error)
	StartContainer(username string, waitForReady bool) (*StartContainerResponse, error)
	StopContainer(username string, force bool) (*StopContainerResponse, error)
	ResizeContainer(username, cpu, memory, disk, memoryRequest, cpuRequest string) (*ResizeContainerResponse, error)
	ToggleMonitoring(username string, enabled bool) (*ToggleMonitoringResponse, error)
	ToggleAutoSleep(username string, enabled bool, idleThresholdMinutes int32) (*ToggleAutoSleepResponse, error)
	GetMetrics(username string) (*GetMetricsResponse, error)

	// Recipes / agents / crews.
	ListRecipes() (*ListRecipesResponse, error)
	DeployRecipe(req DeployRecipeRequest) (*DeployRecipeResponse, error)
	ListAgentSkills() (*ListAgentSkillsResponse, error)
	ListAgentEngines() (*ListAgentEnginesResponse, error)
	RunAgentSkill(req RunAgentSkillRequest) (*RunAgentSkillResponse, error)
	CallAgent(req CallAgentRequest) (*CallAgentResponse, error)
	ListCrews() (*ListCrewsResponse, error)
	RunCrew(req RunCrewRequest) (*RunCrewResponse, error)
	TailRunLog(req *pb.TailRunLogRequest) (*pb.TailRunLogResponse, error)
	ListBoxRuns(username string) ([]*pb.BoxRun, error)
	TailBoxRunLog(req *pb.TailBoxRunLogRequest) (*pb.TailBoxRunLogResponse, error)
	// Permission requests of a box run (#2184).
	ListRunInputs(req *pb.ListRunInputsRequest) (*pb.ListRunInputsResponse, error)
	AnswerRunInput(req *pb.AnswerRunInputRequest) (*pb.AnswerRunInputResponse, error)
	// Coding-tool egress allowlist, read only (#2378).
	GetCodingToolEgressPolicy(req *pb.GetCodingToolEgressPolicyRequest) (*pb.GetCodingToolEgressPolicyResponse, error)

	// Backups.
	CreateBackup(req CreateBackupRequest) (*CreateBackupResponse, error)
	ListBackups(username string) (*ListBackupsResponse, error)
	RestoreBackup(req RestoreBackupRequest) (*RestoreBackupResponse, error)
	VerifyBackup(req VerifyBackupRequest) (*VerifyBackupResponse, error)

	// Secrets / KMS.
	SetSecret(username, name, value string) (*SecretResponse, error)
	GetSecret(username, name string) (string, error)
	ListSecrets(username string) ([]SecretMetadata, error)
	DeleteSecret(username, name string) error
	RefreshSecrets(username string) (*RefreshSecretsResponse, error)
	GetKMSStatus() (*KMSStatusResponse, error)
	GetEnvelopeCoverage() (*EnvelopeCoverageResponse, error)
	MigrateToEnvelope(req MigrateToEnvelopeBody) (*MigrateToEnvelopeResponse, error)
	// Guardrail policy (#2368): read only; the write stays CLI-only.
	GetGuardrailPolicy() (*pb.GetGuardrailPolicyResponse, error)

	// Model gateway (#1726). Mint only: the key verbs are operator-gated and
	// deliberately not exposed to an agent (see gateway_tools.go).
	MintGatewayToken(req MintGatewayTokenBody) (*MintGatewayTokenResponse, error)

	// Network guards (#2353): the tenant ↔ tenant and tenant → core NIC-ACL
	// posture of the backend, read-only.
	GetNetworkGuardStatus() (*NetworkGuardStatusResponse, error)

	// Routes / backends.
	AddRoute(req AddRouteRequest) (*AddRouteResponse, error)
	ListRoutes(username string, activeOnly bool) (*ListRoutesResponse, error)
	DeleteRoute(domain string) error
	AddPassthroughRoute(req AddPassthroughRouteRequest) (*AddPassthroughRouteResponse, error)
	ListPassthroughRoutes() (*ListPassthroughRoutesResponse, error)
	DeletePassthroughRoute(externalPort int32, protocol string) error
	ListBackends() (*ListBackendsResponse, error)
	GetBackend(id string) (*Backend, error)
	ValidateGPU(backendID, pci string) (*ValidateGPUResult, error)

	// Collaborators (#1145) — the agent surface for box access a human
	// already has via the CLI/REST/dashboard.
	AddCollaborator(req AddCollaboratorRequest) (*AddCollaboratorResponse, error)
	ListCollaborators(ownerUsername string) (*ListCollaboratorsResponse, error)
	RemoveCollaborator(ownerUsername, collaboratorUsername string) (*RemoveCollaboratorResponse, error)
	// SSH sessions (#2522), through the CLI's shared client.
	TerminateUserSessions(username string) (*pb.TerminateUserSessionsResponse, error)

	// Security.
	TriggerSecurityScan(kind, containerName, username string) (*SecurityScanResponse, error)
	ListSecurityFindings(kind, containerName string) ([]SecurityFinding, error)
	RemediateSecurityFinding(findingID int64) (*SecurityRemediateResponse, error)

	// Threat-detection sentry (#1640).
	GetSentryStatus() (*SentryStatusResponse, error)

	// Process policy (#2636, #2666): what a HIGH mining finding does to its box.
	GetProcessPolicy(container string) (*pb.GetProcessPolicyResponse, error)
	SetProcessPolicy(req *pb.SetProcessPolicyRequest) (*pb.SetProcessPolicyResponse, error)
	ReleaseProcessAction(container string) (*pb.ReleaseProcessActionResponse, error)

	// Miner-signature list (#2638, #2763).
	ListMinerSignatures() (*pb.ListMinerSignaturesResponse, error)
	AddMinerSignature(req *pb.AddMinerSignatureRequest) (*pb.AddMinerSignatureResponse, error)
	RemoveMinerSignature(kind pb.MinerSignatureKind, pattern string) (*pb.RemoveMinerSignatureResponse, error)

	// Known-bad destination rule (#1641).
	ListBadDestinations() (*ListBadDestinationsResponse, error)
	AddBadDestination(cidr, label string) (*BadDestinationEntry, error)
	RemoveBadDestination(cidr string) error

	// Findings delivery + triage (#1643).
	ListSecuritySentryFindings(severity, tenant, since, state string, limit int) (*ListSentryFindingsResponse, error)
	ResolveSecuritySentryFinding(id int64) (*ResolveSentryFindingResponse, error)

	// Tokens.
	RevokeToken(jti, reason, expiresAt string) (string, error)

	// Tracker broker (#1922) — issue/change-request read and write verbs
	// an agent's run-scoped JWT can hold (tracker:read / tracker:write).
	// Connection CRUD and GetTrackerStatus are tracker:admin — deliberately
	// NOT exposed here, same reasoning as why an agent's token never gets
	// admin scopes for containers or secrets either.
	GetTrackerIssue(req GetTrackerIssueRequest) (*TrackerIssue, error)
	ListTrackerIssues(req ListTrackerIssuesRequest) ([]TrackerIssue, error)
