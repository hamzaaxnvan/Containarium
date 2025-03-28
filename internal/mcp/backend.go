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
