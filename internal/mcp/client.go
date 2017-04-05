package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/footprintai/containarium/pkg/core/incus"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/runlog"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
	"google.golang.org/protobuf/encoding/protojson"
)

// Client is a REST API client for Containarium.
type Client struct {
	baseURL string

	// jwtToken is the static token used when jwtTokenFile is empty.
	// Captured at NewClient time; doesn't survive operator-side
	// rotation without an MCP restart.
	jwtToken string

	// jwtTokenFile, when set, is the path the client reads the JWT
	// from on every request. Operators rotating the token can just
	// overwrite the file in place — the next call picks up the new
	// content. Empty falls back to jwtToken.
	jwtTokenFile string

	httpClient *http.Client

	// tlsConfigErr is set when buildMCPTLSConfig refuses the
	// baseURL (e.g. http:// without ALLOW_INSECURE). doRequest
	// returns this error on every call so the failure is visible
	// to the caller rather than buried in the startup log. Audit
	// C-HIGH-1.
	tlsConfigErr error
}

// NewClient creates a new Containarium REST API client.
//
// Audit C-HIGH-1: the client refuses an http:// baseURL by default,
// because the JWT it carries travels as a Bearer header and any
// passive MITM on the path would harvest the admin token. To opt
// into plaintext for dev/test, set
// CONTAINARIUM_MCP_ALLOW_INSECURE=true; the daemon logs a loud
// WARNING in that mode so an operator can't accidentally leave it
// on in production.
//
// To pin a specific CA (Containarium-issued by Phase 0.5, or a
// corporate CA), set CONTAINARIUM_MCP_TRUSTED_CA_FILE to a PEM
// bundle. Without it, the system trust store is used — fine for
// publicly-trusted hostnames, insufficient for a self-signed
// sentinel cert.
func NewClient(baseURL, jwtToken string) *Client {
	tlsConfig, err := buildMCPTLSConfig(baseURL)
	if err != nil {
		// Log loudly at construction so the misconfig is visible
		// in the daemon's startup output, then stash the error
		// so every doRequest call returns it. NewClient stays
		// non-erroring to preserve the existing call shape.
		log.Printf("[mcp-client] WARNING: TLS config rejected (%v); every request will return this error until baseURL or CA is fixed", err)
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	return &Client{
		baseURL:      baseURL,
		jwtToken:     jwtToken,
		tlsConfigErr: err,
		httpClient: &http.Client{
			Timeout:   120 * time.Second, // Container creation can take time
			Transport: transport,
		},
	}
}

// SetTokenFile switches the client to file-based token mode: every
// request re-reads `path` to get the current JWT. Empty disables
// file mode and falls back to the static jwtToken captured at
// NewClient time. Used by the MCP server when the operator set
// CONTAINARIUM_JWT_TOKEN_FILE.
func (c *Client) SetTokenFile(path string) {
	c.jwtTokenFile = path
}

// readToken returns the JWT to use for the next request. When a
// tokenFile is configured, reads it fresh from disk (whitespace
// trimmed) on every call so token rotation works without a restart.
// Otherwise returns the static token captured at construction.
//
// Audit C-HIGH-7: the file must be mode 0600 or stricter, otherwise
// any unprivileged user on the host can read the admin JWT. Fail
// closed and surface the actual mode in the error so an operator
// can chmod it without guessing.
func (c *Client) readToken() (string, error) {
	if c.jwtTokenFile == "" {
		return c.jwtToken, nil
	}
	info, err := os.Stat(c.jwtTokenFile)
	if err != nil {
		return "", fmt.Errorf("stat JWT file %s: %w", c.jwtTokenFile, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return "", fmt.Errorf("JWT file %s has insecure permissions %#o (any non-owner read/write bit set); chmod 0600 it", c.jwtTokenFile, mode)
	}
	b, err := os.ReadFile(c.jwtTokenFile) // #nosec G304 -- path is operator config (CONTAINARIUM_JWT_TOKEN_FILE), already perm-checked
	if err != nil {
		return "", fmt.Errorf("read JWT from %s: %w", c.jwtTokenFile, err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("JWT file %s is empty", c.jwtTokenFile)
	}
	return token, nil
}

// doRequest performs an HTTP request with JWT authentication
func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	if c.tlsConfigErr != nil {
		return nil, fmt.Errorf("MCP client refuses to send request: %w", c.tlsConfigErr)
	}
	url := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		// A []byte body is already-encoded JSON — pass it through as-is.
		// Marshaling it again would wrap the bytes as a base64 JSON
		// string, which the daemon's grpc-gateway decoder rejects as a
		// proto syntax error. Several callers (ToggleMonitoring,
		// SetSecret, ResizeContainer, RefreshSecrets) pre-marshal; this
		// keeps them correct rather than double-encoding. See #370.
		jsonData, ok := body.([]byte)
		if !ok {
			var err error
			jsonData, err = json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request: %w", err)
			}
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add JWT token. readToken() may re-read from disk if a token file
	// was configured — that's the file-rotation-without-restart path.
	token, err := c.readToken()
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	// Advertise the client version so the daemon can log it and, if it
	// chooses, gate on a minimum-supported client. Both the conventional
	// User-Agent and the explicit header are set; a server reads whichever
	// it prefers.
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.ClientVersionHeader, version.GetVersion())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &client.HTTPStatusError{Status: resp.StatusCode, Body: string(respBody)}
	}

	return respBody, nil
}

// CreateContainer creates a new container
func (c *Client) CreateContainer(req CreateContainerRequest) (*CreateContainerResponse, error) {
	if req.NetworkTenant != "" {
		caps, err := c.GetNetworkTenantCapabilities()
		if err != nil {
			return nil, fmt.Errorf("native Tenant capability check: %w", err)
		}
		if caps.Contract != incus.NativeTenantContractID || caps.BackendScope != incus.NativeTenantBackendScope || !caps.NativeMembershipRead || !caps.SharedTenantCreate {
			return nil, fmt.Errorf("provider does not advertise ready native network Tenant creation")
		}
	}

	respBody, err := c.doRequest("POST", "/v1/containers", req)
	if err != nil {
		return nil, err
	}

	var resp CreateContainerResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}

// ListContainers lists all containers
func (c *Client) ListContainers() (*ListContainersResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/containers", nil)
	if err != nil {
		return nil, err
	}

	var resp ListContainersResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}

// GetContainer gets a specific container
func (c *Client) GetContainer(username string) (*GetContainerResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/containers/"+username, nil)
	if err != nil {
		return nil, err
	}

	var resp GetContainerResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}

// RecipeSummary is the subset of a recipe the MCP surface displays.
type RecipeSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
	RequiresGPU bool   `json:"requiresGpu"`
}

// ListRecipesResponse is the /v1/recipes response.
type ListRecipesResponse struct {
	Recipes []RecipeSummary `json:"recipes"`
}

// ListRecipes returns the daemon's built-in recipe catalog.
func (c *Client) ListRecipes() (*ListRecipesResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/recipes", nil)
	if err != nil {
		return nil, err
	}
	var resp ListRecipesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// DeployRecipeRequest is the body for a recipe deploy.
type DeployRecipeRequest struct {
	RecipeID       string                 `json:"recipe_id"`
	Name           string                 `json:"name"`
	GPU            string                 `json:"gpu,omitempty"`
	BackendID      string                 `json:"backend_id,omitempty"`
	Pool           string                 `json:"pool,omitempty"`
	Parameters     map[string]string      `json:"parameters,omitempty"`
	GuardrailInput *pb.GuardrailGateInput `json:"guardrail_input,omitempty"`
}

// DeployRecipeContainer is the deployed container's MCP summary.
type DeployRecipeContainer struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// DeployRecipeResponse is the result of a recipe deploy.
type DeployRecipeResponse struct {
	URL       string                 `json:"url"`
	Message   string                 `json:"message"`
	Container *DeployRecipeContainer `json:"container"`
}

// DeployRecipe wraps the CLI's typed HTTP client, retaining MCP's TLS
// restrictions, configured transport and per-request token-file rotation.
func (c *Client) DeployRecipe(req DeployRecipeRequest) (*DeployRecipeResponse, error) {
	if c.tlsConfigErr != nil {
		return nil, fmt.Errorf("MCP client refuses to send request: %w", c.tlsConfigErr)
	}
	token, err := c.readToken()
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	typed, err := client.NewHTTPClient(c.baseURL, token, client.WithHTTPClient(c.httpClient))
	if err != nil {
		return nil, err
	}
	resp, err := typed.DeployRecipe(req.RecipeID, req.Name, req.GPU, req.BackendID, req.Pool, req.Parameters, req.GuardrailInput)
	if err != nil {
		return nil, err
	}
	out := &DeployRecipeResponse{URL: resp.GetUrl(), Message: resp.GetMessage()}
	if container := resp.GetContainer(); container != nil {
		out.Container = &DeployRecipeContainer{Name: container.GetName(), State: container.GetState().String()}
	}
	return out, nil
}

// --- agent skills (AgentSkillService) ---

// SkillSummary is one entry of the /v1/agent-skills response. JSON tags are
// the grpc-gateway camelCase output names.
type SkillSummary struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	RecipeID      string   `json:"recipeId"`
	AllowedScopes []string `json:"allowedScopes"`
}

// ListAgentSkillsResponse is the /v1/agent-skills response.
type ListAgentSkillsResponse struct {
	Skills []SkillSummary `json:"skills"`
}

// ListAgentSkills returns the daemon's built-in agent-skill catalog.
func (c *Client) ListAgentSkills() (*ListAgentSkillsResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/agent-skills", nil)
	if err != nil {
		return nil, err
	}
	var resp ListAgentSkillsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// AgentEngineStatusSummary is one entry of the /v1/agent-engines response.
// JSON tags are the grpc-gateway camelCase output names; Engine/Provider/
// Readiness/Source arrive as the proto enum's NAME string (e.g.
// "AGENT_ENGINE_CLAUDE"), which is grpc-gateway's default enum encoding.
type AgentEngineStatusSummary struct {
	Engine    string   `json:"engine"`
	Provider  string   `json:"provider"`
	Readiness string   `json:"readiness"`
	Reason    string   `json:"reason"`
	Source    string   `json:"source"`
	IsDefault bool     `json:"isDefault"`
	SkillIDs  []string `json:"skillIds"`
}

// ListAgentEnginesResponse is the /v1/agent-engines response (#2223).
type ListAgentEnginesResponse struct {
	Engines  []AgentEngineStatusSummary `json:"engines"`
	KeyOwner string                     `json:"keyOwner"`
}

// ListAgentEngines reports, for each agent engine, whether a run naming it
// would be refused right now — the same check RunAgentSkill's refusal
// enforces, read-only.
func (c *Client) ListAgentEngines() (*ListAgentEnginesResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/agent-engines", nil)
	if err != nil {
		return nil, err
	}
	var resp ListAgentEnginesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// RunAgentSkillRequest is the body for an agent-skill run. Snake_case tags
// match the proto field names, which grpc-gateway accepts on input.
// GitSource/GitRef (#1859) fetch a repo into the run's workspace before the
// agent starts; no credential field over MCP in this phase (see the OSS
// issue's scope note).
type RunAgentSkillRequest struct {
	SkillID   string `json:"skill_id"`
	BackendID string `json:"backend_id,omitempty"`
	Pool      string `json:"pool,omitempty"`
	InputJSON string `json:"input_json,omitempty"`
	GitSource string `json:"git_source,omitempty"`
	GitRef    string `json:"git_ref,omitempty"`
	// Engine (#2228) is the proto enum's NAME string (e.g.
	// "AGENT_ENGINE_CODEX"), the shape protojson expects for an enum field.
	// handleRunAgentSkill validates+normalizes the tool's lowercase "engine"
	// argument into this shape before it ever reaches here.
	Engine string `json:"engine,omitempty"`
}

// RunAgentSkillResponse is the result of an agent-skill run.
type RunAgentSkillResponse struct {
	ArtifactJSON string `json:"artifactJson"`
	// RunID is the run's handle; follow it with crew_logs.
	RunID string `json:"runId,omitempty"`
	// Message is free-form status from the server, e.g. how to follow a run
	// still in flight on a server that runs skills asynchronously.
	Message       string `json:"message,omitempty"`
	GitCommit     string `json:"gitCommit,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Container     *struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"container"`
}

// RunAgentSkill provisions a skill's box, mints a scoped token, seeds the task,
// and returns the box.
func (c *Client) RunAgentSkill(req RunAgentSkillRequest) (*RunAgentSkillResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/agent-skills/"+req.SkillID+"/run", req)
	if err != nil {
		return nil, err
	}
	var resp RunAgentSkillResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// CallAgentRequest is the body for an A2A task delegation. Snake_case tags
// match the proto field names, which grpc-gateway accepts on input.
type CallAgentRequest struct {
	FromSkillID string `json:"from_skill_id,omitempty"`
	ToPeerID    string `json:"to_peer_id"`
	InputJSON   string `json:"input_json,omitempty"`
}

// CallAgentResponse is the result of an A2A task delegation.
type CallAgentResponse struct {
	Artifact *struct {
		TaskID     string `json:"taskId"`
		OutputJSON string `json:"outputJson"`
		State      string `json:"state"`
		Error      string `json:"error"`
	} `json:"artifact"`
}

// CallAgent delegates a task to a running peer agent over A2A.
func (c *Client) CallAgent(req CallAgentRequest) (*CallAgentResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/agent-skills/"+req.ToPeerID+"/call", req)
	if err != nil {
		return nil, err
	}
	var resp CallAgentResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// --- crews (CrewService) ---

// CrewSummary is one entry of the /v1/crews response (grpc-gateway camelCase).
type CrewSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Topology    string   `json:"topology"`
	SkillIDs    []string `json:"skillIds"`
}

// ListCrewsResponse is the /v1/crews response.
type ListCrewsResponse struct {
	Crews []CrewSummary `json:"crews"`
}

// ListCrews returns the daemon's built-in crew catalog.
func (c *Client) ListCrews() (*ListCrewsResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/crews", nil)
	if err != nil {
		return nil, err
	}
	var resp ListCrewsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// RunCrewRequest is the body for a crew run (snake_case for grpc-gateway input).
type RunCrewRequest struct {
	CrewID    string `json:"crew_id"`
	BackendID string `json:"backend_id,omitempty"`
	Pool      string `json:"pool,omitempty"`
	InputJSON string `json:"input_json,omitempty"`
	// ModelOverrides maps skill_id -> model id (#3259), passed through as is;
	// the daemon refuses the unusable ones.
	ModelOverrides map[string]string `json:"model_overrides,omitempty"`
}

// RunCrewResponse is the result of a crew run.
type RunCrewResponse struct {
	Run *struct {
		ID      string `json:"id"`
		CrewID  string `json:"crewId"`
		TraceID string `json:"traceId"`
		State   string `json:"state"`
		Error   string `json:"error"`
	} `json:"run"`
}

// RunCrew launches a crew: validates topology, provisions member boxes, returns
// the run handle.
func (c *Client) RunCrew(req RunCrewRequest) (*RunCrewResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/crews/"+req.CrewID+"/run", req)
	if err != nil {
		return nil, err
	}
	var resp RunCrewResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// TailRunLog reads one window of a run's journal (#2096).
func (c *Client) TailRunLog(req *pb.TailRunLogRequest) (*pb.TailRunLogResponse, error) {
	respBody, err := c.doRequest("GET", runlog.Query(req), nil)
	if err != nil {
		return nil, err
	}
	out := &pb.TailRunLogResponse{}
	if err := protojson.Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return out, nil
}

// ListBoxRuns lists a box's code-run records (#2123).
func (c *Client) ListBoxRuns(username string) ([]*pb.BoxRun, error) {
	respBody, err := c.doRequest("GET", runlog.BoxRunsPath(username), nil)
	if err != nil {
		return nil, err
	}
	out := &pb.ListBoxRunsResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return out.GetRuns(), nil
}

// TailBoxRunLog reads one window of a box run's log (#2123).
func (c *Client) TailBoxRunLog(req *pb.TailBoxRunLogRequest) (*pb.TailBoxRunLogResponse, error) {
	respBody, err := c.doRequest("GET", runlog.BoxQuery(req), nil)
	if err != nil {
		return nil, err
	}
	out := &pb.TailBoxRunLogResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return out, nil
}

// ListRunInputs lists a box run's open permission requests (#2184).
func (c *Client) ListRunInputs(req *pb.ListRunInputsRequest) (*pb.ListRunInputsResponse, error) {
	respBody, err := c.doRequest("GET", runlog.RunInputsPath(req.GetUsername(), req.GetRunName()), nil)
	if err != nil {
		return nil, err
	}
	out := &pb.ListRunInputsResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return out, nil
}

// AnswerRunInput answers one permission request of a box run (#2184).
func (c *Client) AnswerRunInput(req *pb.AnswerRunInputRequest) (*pb.AnswerRunInputResponse, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}
	respBody, err := c.doRequest("POST", runlog.RunInputAnswerPath(req.GetUsername(), req.GetRunName(), req.GetRequestId()), body)
	if err != nil {
		return nil, err
	}
	out := &pb.AnswerRunInputResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return out, nil
}

// --- database backups (BackupService) ---

// PgConnectionBody carries pg_dump/pg_restore connection params. Snake_case
// tags match the proto field names, which grpc-gateway accepts on input.
type PgConnectionBody struct {
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int32  `json:"port,omitempty"`
}

// CreateBackupRequest is the body for POST /v1/backups. Destination is the
// proto enum NAME ("BACKUP_DESTINATION_LOCAL" / "BACKUP_DESTINATION_GCS").
type CreateBackupRequest struct {
	Username    string            `json:"username"`
	Connection  *PgConnectionBody `json:"connection,omitempty"`
	Destination string            `json:"destination"`
	GCSBucket   string            `json:"gcs_bucket,omitempty"`

	// #1831: credential-less in-tenant hook and user-held encryption.
	Hook         string `json:"hook,omitempty"`
	Label        string `json:"label,omitempty"`
	AgeRecipient string `json:"age_recipient,omitempty"`

	// #2402: the proto enum NAME ("BACKUP_KEY_MODE_MANAGED", ...). Empty
	// is UNSPECIFIED — the daemon default.
	KeyMode string `json:"key_mode,omitempty"`

	// #2405: declared hook output format, as the pb.HookFormat value NAME
	// (what protojson decodes). Empty is omitted, which the daemon reads
	// as opaque.
	HookFormat string `json:"hook_format,omitempty"`

	// #3214: refuse (FAILED_PRECONDITION) a backup that would be stored
	// unencrypted; mirrors `backup create --require-encryption` (#3169).
	RequireEncryption bool `json:"require_encryption,omitempty"`
}

// BackupRecord mirrors the proto BackupRecord on the response side
// (proto JSON → lowerCamelCase field names).
type BackupRecord struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Database    string `json:"database"`
	CreatedAt   string `json:"createdAt"`
	SizeBytes   string `json:"sizeBytes"` // int64 is encoded as a string in proto JSON
	SHA256      string `json:"sha256"`
	Destination string `json:"destination"`
	Location    string `json:"location"`
	Engine      string `json:"engine"`

	LastVerification *BackupVerification `json:"lastVerification,omitempty"`

	// #1831
	Encrypted    bool   `json:"encrypted,omitempty"`
	AgeRecipient string `json:"ageRecipient,omitempty"`
	Hook         string `json:"hook,omitempty"`

	// #2402: the proto enum NAME; bytes are base64 in proto JSON. The
	// daemon never unwraps WrappedKey — whoever restores does.
	WrappedKey string `json:"wrappedKey,omitempty"`
	KekID      string `json:"kekId,omitempty"`
	KeyMode    string `json:"keyMode,omitempty"`
	HookFormat string `json:"hookFormat,omitempty"` // #2405, enum value name
}

// VerificationCheck is one assertion made during a restore test.
type VerificationCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// BackupVerification mirrors the proto BackupVerification: the audit
// artifact for one restore test.
type BackupVerification struct {
	VerifiedAt      string              `json:"verifiedAt"`
	Result          string              `json:"result"`
	Error           string              `json:"error,omitempty"`
	TargetContainer string              `json:"targetContainer"`
	ScratchDatabase string              `json:"scratchDatabase"`
	DurationMS      string              `json:"durationMs"` // int64 → string in proto JSON
	VerifiedBy      string              `json:"verifiedBy,omitempty"`
	Checks          []VerificationCheck `json:"checks,omitempty"`
}

// CreateBackupResponse is the result of a backup create.
type CreateBackupResponse struct {
	Message string        `json:"message"`
	Record  *BackupRecord `json:"record"`
}

// ListBackupsResponse is the /v1/backups response.
type ListBackupsResponse struct {
	Records []BackupRecord `json:"records"`
}

// RestoreBackupRequest is the body for POST /v1/backups/{id}/restore.
type RestoreBackupRequest struct {
	ID         string            `json:"id"`
	Connection *PgConnectionBody `json:"connection,omitempty"`
	Clean      bool              `json:"clean,omitempty"`
	// AgeIdentity decrypts an encrypted record for this one call (#1831).
	AgeIdentity string `json:"age_identity,omitempty"`
	// TargetContainer restores into another tenant container; empty is
	// the backup's own container (#2403).
	TargetContainer string `json:"target_container,omitempty"`
}

// RestoreBackupResponse is the result of a restore.
type RestoreBackupResponse struct {
	Message string `json:"message"`
}

// CreateBackup dumps a tenant's database and stores it off-host.
func (c *Client) CreateBackup(req CreateBackupRequest) (*CreateBackupResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/backups", req)
	if err != nil {
		return nil, err
	}
	var resp CreateBackupResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// ListBackups lists stored backups, optionally filtered by tenant.
func (c *Client) ListBackups(username string) (*ListBackupsResponse, error) {
	path := "/v1/backups"
	if username != "" {
		path += "?username=" + url.QueryEscape(username)
	}
	respBody, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp ListBackupsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// VerifyBackupRequest is the body for POST /v1/backups/{id}/verify.
type VerifyBackupRequest struct {
	ID             string            `json:"id"`
	TargetUsername string            `json:"target_username"`
	Connection     *PgConnectionBody `json:"connection,omitempty"`
	// AgeIdentity decrypts an encrypted record for this one call, same
	// contract as RestoreBackupRequest.AgeIdentity (#1831, #2295).
	AgeIdentity string `json:"age_identity,omitempty"`
}

// VerifyBackupResponse is the result of a restore test.
type VerifyBackupResponse struct {
	Message      string              `json:"message"`
	Verification *BackupVerification `json:"verification"`
	Record       *BackupRecord       `json:"record"`
}

// VerifyBackup restore-tests a stored dump against a throwaway target.
func (c *Client) VerifyBackup(req VerifyBackupRequest) (*VerifyBackupResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/backups/"+req.ID+"/verify", req)
	if err != nil {
		return nil, err
	}
	var resp VerifyBackupResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// RestoreBackup streams a stored dump back into a container's database.
func (c *Client) RestoreBackup(req RestoreBackupRequest) (*RestoreBackupResponse, error) {
	respBody, err := c.doRequest("POST", "/v1/backups/"+req.ID+"/restore", req)
	if err != nil {
		return nil, err
	}
	var resp RestoreBackupResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &resp, nil
}

// --- KMS admin (KmsService) ---

// KMSStatusResponse is the /v1/kms/status response.
type KMSStatusResponse struct {
	Backend         string `json:"backend"`
	Description     string `json:"description"`
	KmsConfigured   bool   `json:"kmsConfigured"`
	RequireEnvelope bool   `json:"requireEnvelope"`
}

// EnvelopeCoverageResponse is the /v1/kms/envelope-coverage response.
// Counts are int64, encoded as strings in proto JSON.
type EnvelopeCoverageResponse struct {
	Total    string `json:"total"`
	Legacy   string `json:"legacy"`
	Envelope string `json:"envelope"`
}

// MigrateToEnvelopeBody is the POST /v1/kms/migrate-to-envelope body.
type MigrateToEnvelopeBody struct {
	DryRun  bool  `json:"dryRun,omitempty"`
	MaxRows int64 `json:"maxRows,omitempty"`
}

// MigrateRowError names a row that failed migration.
type MigrateRowError struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Error    string `json:"error"`
}

// MigrateToEnvelopeResponse is the migration result. Counts are
// int64, encoded as strings in proto JSON.
type MigrateToEnvelopeResponse struct {
	Scanned     string            `json:"scanned"`
	Migrated    string            `json:"migrated"`
	AlreadyDone string            `json:"alreadyDone"`
	Failed      string            `json:"failed"`
	DryRun      bool              `json:"dryRun"`
	Errors      []MigrateRowError `json:"errors"`
}

// GetSentryStatus reports the threat-detection sentry's on/off state and
// per-rule health (#1640).
func (c *Client) GetSentryStatus() (*SentryStatusResponse, error) {
	respBody, err := c.doRequest("GET", "/v1/security/sentry/status", nil)
