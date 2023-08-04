package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/runlog"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// HTTPClient wraps an HTTP connection to the containarium REST API
type HTTPClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// HTTPClientOption configures the HTTP transport used by the typed client.
type HTTPClientOption func(*HTTPClient)

// WithHTTPClient reuses a caller's transport, TLS trust and timeouts. The
// caller must finish configuring it before making requests.
func WithHTTPClient(httpClient *http.Client) HTTPClientOption {
	return func(c *HTTPClient) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// NewHTTPClient creates a new HTTP client
func NewHTTPClient(baseURL string, token string, options ...HTTPClientOption) (*HTTPClient, error) {
	if baseURL == "" {
		return nil, ErrNoServerConfigured
	}

	// Ensure baseURL has proper format
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	// Remove trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Pin HTTP/1.1. Go's default client negotiates HTTP/2 via ALPN against
	// an HTTPS server, but a long-running request (a container create can
	// take minutes) carried on an HTTP/2 connection through a fronting TLS
	// edge intermittently gets the connection reset with
	// `remote error: tls: internal error` — the server still completes the
	// work (the box is provisioned), only the response connection dies. The
	// identical request over HTTP/1.1 (e.g. curl's default) is clean. This
	// REST client issues one request per process, so HTTP/2 multiplexing
	// buys nothing here. Clearing TLSNextProto on a cloned default transport
	// is the documented way to force HTTP/1.1 on net/http while keeping the
	// default dial/timeout/proxy behaviour. See FootprintAI/Containarium#422.
	//
	// Clearing TLSNextProto only removes the h2 *handler* — it does NOT
	// constrain ALPN. A fronting edge that defaults to h2 when the client
	// advertises no ALPN protocols (e.g. Cloudflare) then still negotiates
	// HTTP/2, the server sends h2 frames, and the h1 parser chokes on them:
	// `malformed HTTP response "\x00\x00\x12\x04..."` (an h2 SETTINGS frame),
	// deterministically. Pin the ALPN offer to http/1.1 so the edge serves
	// HTTP/1.1 — matching the clean `curl --http1.1` path.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(authority string, c *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	}

	client := &HTTPClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout:   15 * time.Minute, // Match gRPC client timeout for container creation
			Transport: transport,
		},
	}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

// Close closes the HTTP client (no-op for HTTP, kept for interface compatibility)
func (c *HTTPClient) Close() error {
	return nil
}

// doRequest performs an HTTP request with authentication
// body is the encoded JSON request body, or nil for a bodyless request.
//
// It is []byte rather than interface{} deliberately. The old signature
// marshalled whatever it was handed, so handing it an already-encoded
// []byte silently base64-encoded the payload into a JSON string and the
// daemon rejected it with `proto: syntax error (line 1:1): unexpected
// token "<base64>"` — three client methods shipped broken that way
// (#887). Taking bytes makes that mistake unrepresentable: callers encode
// exactly once, at a site where the payload's type is known.
func (c *HTTPClient) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// Advertise the client version so the daemon can log it and, if it
	// chooses, gate on a minimum-supported client. Both the conventional
	// User-Agent and the explicit header are set; a server reads whichever
	// it prefers.
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.ClientVersionHeader, version.GetVersion())

	return c.httpClient.Do(req)
}

// drainClose fully drains then closes an HTTP response body. Draining
// before Close matters for HTTP/2: a body closed while still unread makes
// Go send a RST_STREAM (plus a PING) to cancel the stream, which
// Cloudflare and similar edges treat as abusive client behaviour and
// answer by tearing down the whole connection (GOAWAY ENHANCE_YOUR_CALM)
// — see FootprintAI/Containarium#422 and
// https://blog.cloudflare.com/go-and-enhance-your-calm/. This REST client
// is pinned to HTTP/1.1 today (where the concern is moot), but draining on
// every path keeps it correct if HTTP/2 is ever re-enabled, and it's a
// no-op cost when the caller already read the body to EOF.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// parseResponse reads and parses the response body
func parseResponse[T any](resp *http.Response) (*T, error) {
	defer drainClose(resp)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to parse error response
		var errResp struct {
			Error string `json:"error"`
			Code  int    `json:"code"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result T
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// Container response types matching the protobuf JSON output
type containerResponse struct {
	Name                 string                    `json:"name"`
	Username             string                    `json:"username"`
	SshHost              string                    `json:"sshHost"`
	State                string                    `json:"state"`
	Resources            *resourceLimits           `json:"resources"`
	Network              *networkInfo              `json:"network"`
	CreatedAt            string                    `json:"createdAt"`
	Labels               map[string]string         `json:"labels"`
	NetworkTenant        string                    `json:"networkTenant"`
	NetworkTenantSource  incus.NetworkTenantSource `json:"networkTenantSource"`
	Image                string                    `json:"image"`
	PodmanEnabled        bool                      `json:"podmanEnabled"`
	GpuDevice            string                    `json:"gpuDevice"`
	GpuDevices           []string                  `json:"gpuDevices"`
	Isolation            string                    `json:"isolation"`  // protojson enum name, e.g. ISOLATION_TYPE_VM
	ToolAccess           string                    `json:"toolAccess"` // protojson enum name, e.g. BOX_TOOL_ACCESS_NONE
	MonitoringEnabled    bool                      `json:"monitoringEnabled"`
	AutoSleepEnabled     bool                      `json:"autoSleepEnabled"`
	IdleThresholdMinutes int32                     `json:"idleThresholdMinutes"`
}

type resourceLimits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Disk   string `json:"disk"`
}

type networkInfo struct {
	IPAddress  string `json:"ipAddress"`
	MACAddress string `json:"macAddress"`
	Interface  string `json:"interface"`
	Bridge     string `json:"bridge"`
}

type listContainersResponse struct {
	Containers []containerResponse `json:"containers"`
}

type createContainerResponse struct {
	Container *containerResponse `json:"container"`
	// DryRun echoes a validate-only request (#2925); Container is nil then.
	DryRun bool `json:"dryRun"`
}

type getContainerResponse struct {
	Container *containerResponse `json:"container"`
}

// containerToIncusInfo converts API response to incus.ContainerInfo
func containerToIncusInfo(c *containerResponse) incus.ContainerInfo {
	info := incus.ContainerInfo{
		Name:     c.Name,
		Username: c.Username,
		SSHHost:  c.SshHost,
		State:    c.State,
		Labels:   c.Labels,
		Tenant:   c.NetworkTenant, NetworkTenantSource: c.NetworkTenantSource,
		InstanceType:         ostype.InstanceTypeFromIsolation(pb.IsolationType(pb.IsolationType_value[c.Isolation])),
		MonitoringEnabled:    c.MonitoringEnabled,
		AutoSleepEnabled:     c.AutoSleepEnabled,
		IdleThresholdMinutes: c.IdleThresholdMinutes,
		PodmanEnabled:        c.PodmanEnabled,
	}

	if c.Network != nil {
		info.IPAddress = c.Network.IPAddress
	}

	if c.Resources != nil {
		info.CPU = c.Resources.CPU
		info.Memory = c.Resources.Memory
	}

	info.GPU = c.GpuDevice
	info.GPUs = c.GpuDevices
	if lvl, ok := pb.BoxToolAccess_value[c.ToolAccess]; ok {
		if v, ok := toolaccess.ConfigValue(pb.BoxToolAccess(lvl)); ok {
			info.ToolAccess = v
		}
	}

	info.CreatedAt = parseCreatedAt(c.CreatedAt)

	return info
}

// parseCreatedAt parses the wire `createdAt` field, which is the Container
// proto's int64 Unix-seconds field (#2146) — protojson renders an int64 as
// a quoted decimal string (e.g. "1738000000"), not RFC3339. RFC3339 is also
// accepted so the client keeps working if the wire format ever switches to
// google.protobuf.Timestamp. Empty or unparseable input leaves the zero
// time, same as before this fix.
func parseCreatedAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(sec, 0)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// ListContainers lists all containers via HTTP
func (c *HTTPClient) ListContainers() ([]incus.ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.doRequest(ctx, http.MethodGet, "/v1/containers", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result, err := parseResponse[listContainersResponse](resp)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var containers []incus.ContainerInfo
	for _, container := range result.Containers {
		containers = append(containers, containerToIncusInfo(&container))
	}

	return containers, nil
}

// CreateContainer creates a container via HTTP
// GitSourceOpts carries the optional create-time git provisioning
// parameters. Zero value (Source == "") means "no git source" and the
// daemon skips provisioning. Kept as a struct so CreateContainer's
// already-long signature doesn't grow four more positional strings.
// EncryptionOpts carries the per-tenant dataset-encryption request
// (#1198). Bundled into a struct rather than added as two more
// positional parameters: CreateContainer already takes eighteen, and a
// bare `encrypted bool` would sit next to `monitoring bool` where a
// transposed argument compiles cleanly and silently creates the wrong
// container.
type EncryptionOpts struct {
	// Encrypted requests a tenant-scoped ZFS key for this container's
	// dataset. The daemon rejects it with FAILED_PRECONDITION when no
	// KeyProvider is configured — it never falls back to plaintext.
	Encrypted bool
	// TenantID scopes the key. Empty or "default" on a single-tenant
	// daemon; any other value is rejected there with INVALID_ARGUMENT.
	TenantID string
}

type GitSourceOpts struct {
	Source        string // clone URL; empty disables git provisioning
	Ref           string // SHA / branch / tag / refs/pull/N/merge; empty = default branch
	Credential    string // bearer token for private repos
	WorkspacePath string // empty defaults to /workspace
}

func (c *HTTPClient) CreateContainer(username, image, cpu, memory, disk string, sshKeys []string, enablePodman bool, stack string, gpus []string, osType pb.OSType, isolation pb.IsolationType, monitoring bool, pool, backendID string, git GitSourceOpts, ttlSeconds int64, idleStopMinutes int32, disableAutoSleep bool, deleteAfterStoppedSeconds int64, storageClass string, enc EncryptionOpts, memoryRequest, cpuRequest, region string, labels map[string]string, networkTenant ...string) (*incus.ContainerInfo, error) {
	if len(networkTenant) > 0 && networkTenant[0] != "" {
		caps, err := c.GetNetworkTenantCapabilities()
		if err != nil {
			return nil, fmt.Errorf("native Tenant capability check: %w", err)
		}
		if err := requireNativeTenantCapability(caps); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req := createContainerRequest{
		Username: username,
		Resources: containerResources{
			CPU:           cpu,
			Memory:        memory,
			Disk:          disk,
			StorageClass:  storageClass,
			MemoryRequest: memoryRequest,
			CPURequest:    cpuRequest,
		},
		SSHKeys:      sshKeys,
		Image:        image,
		EnablePodman: enablePodman,
		Stack:        stack,
		GPUs:         gpus,
		OSType:       osType,
		Isolation:    isolation,
		Monitoring:   monitoring,
		Pool:         pool,
		BackendID:    backendID,
		Encrypted:    enc.Encrypted,
		TenantID:     enc.TenantID,
		Region:       region,
	}
	if len(networkTenant) > 0 {
		req.NetworkTenant = networkTenant[0]
	}
	if len(labels) > 0 {
		req.Labels = labels
	}
	// The four git fields travel together: all present (even when empty)
	// when a source is given, all absent otherwise.
	if git.Source != "" {
		req.GitSource = &git.Source
		req.GitRef = &git.Ref
		req.GitCredential = &git.Credential
		req.WorkspacePath = &git.WorkspacePath
	}
	// Birth TTL (#523), idle-stop (#524), stopped→delete (#525): only
	// included when set, so an unset value stays absent on the wire
	// (0 = disabled) rather than forcing a key the daemon would treat the
	// same but that needlessly differs from a plain create's body.
	if ttlSeconds > 0 {
		req.TTLSeconds = ttlSeconds
	}
	req.DisableAutoSleep = disableAutoSleep
	if idleStopMinutes > 0 {
		req.IdleStopMinutes = idleStopMinutes
	}
	if deleteAfterStoppedSeconds > 0 {
		req.DeleteAfterStoppedSeconds = deleteAfterStoppedSeconds
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/v1/containers", reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	result, err := parseResponse[createContainerResponse](resp)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if result.Container == nil {
		return nil, fmt.Errorf("no container returned in response")
	}

	info := containerToIncusInfo(result.Container)
	return &info, nil
}

// ValidateCreate is GRPCClient.ValidateCreate over the REST API (#2925): it
// POSTs req with dryRun set and requires the daemon to echo it. The body is the
// request message itself (protojson, the camelCase names grpc-gateway accepts),
// not a second hand-written copy of the create fields.
func (c *HTTPClient) ValidateCreate(req *pb.CreateContainerRequest) error {
	if req.GetNetworkTenant() != "" {
		caps, err := c.GetNetworkTenantCapabilities()
		if err != nil {
			return fmt.Errorf("native Tenant capability check: %w", err)
		}
		if err := requireNativeTenantCapability(caps); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dry, ok := proto.Clone(req).(*pb.CreateContainerRequest)
	if !ok {
		return fmt.Errorf("clone create request: unexpected type")
	}
	dry.DryRun = true
	body, err := protojson.Marshal(dry)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/v1/containers", body)
	if err != nil {
		return fmt.Errorf("pre-flight check failed: %w", err)
	}
	result, err := parseResponse[createContainerResponse](resp)
	if err != nil {
		return fmt.Errorf("pre-flight check failed: %w", err)
	}
	if !result.DryRun {
		return errDryRunNotHonoured
	}
	return nil
}

// ToggleAutoSleep writes the per-container auto-sleep opt-in
// metadata via HTTP. See GRPCClient.ToggleAutoSleep for semantics.
func (c *HTTPClient) ToggleAutoSleep(username string, enabled bool, idleThresholdMinutes int32) (*pb.ToggleAutoSleepResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/auto-sleep", url.PathEscape(username))
	body, err := json.Marshal(toggleAutoSleepRequest{
		Enabled:              enabled,
		IdleThresholdMinutes: idleThresholdMinutes,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("toggle auto-sleep: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("toggle auto-sleep: status %d", resp.StatusCode)
	}

	out := &pb.ToggleAutoSleepResponse{}
	if err := protojson.Unmarshal(bodyBytes, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// SetContainerTTL schedules (durationSeconds > 0) or clears (== 0) a
// container's auto-delete TTL via the REST shim. Used by the containarium-run
// keep-on-failure path so a kept debug box self-reaps (#264 TTL-404).
//
// A 404 (a daemon too old to expose /v1/containers/{name}/ttl) is mapped to
// gRPC codes.Unimplemented so the `containarium ttl` CLI's isUnimplemented
// soft-path fires identically across both transports — the box won't auto-
// delete on an old daemon, but the Action doesn't hard-fail.
func (c *HTTPClient) SetContainerTTL(username string, durationSeconds int64) (*pb.SetContainerTTLResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/ttl", url.PathEscape(username))
	body, err := json.Marshal(setContainerTTLRequest{DurationSeconds: durationSeconds})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("set ttl: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, status.Errorf(codes.Unimplemented, "server does not expose SetContainerTTL (HTTP 404)")
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("set ttl: status %d", resp.StatusCode)
	}

	out := &pb.SetContainerTTLResponse{}
	if err := protojson.Unmarshal(bodyBytes, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// SetContainerDeletePolicy protects (DELETE_POLICY_PROTECTED) or unprotects
// (DELETE_POLICY_UNSPECIFIED) a container from the daemon's automated/bulk
// deletion paths via the REST shim (#284).
//
// A 404 (a daemon too old to expose /v1/containers/{name}/delete-policy) is
// mapped to gRPC codes.Unimplemented so the `containarium protect` CLI's
// soft-path fires identically across both transports.
func (c *HTTPClient) SetContainerDeletePolicy(username string, policy pb.DeletePolicy) (*pb.SetContainerDeletePolicyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/delete-policy", url.PathEscape(username))
	// Emit the enum by its proto JSON name so grpc-gateway decodes it into the
	// DeletePolicy enum (it accepts the string form; a bare int would also work
	// but the name is self-documenting on the wire).
	body, err := json.Marshal(setContainerDeletePolicyRequest{DeletePolicy: policy.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("set delete policy: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, status.Errorf(codes.Unimplemented, "server does not expose SetContainerDeletePolicy (HTTP 404)")
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("set delete policy: status %d", resp.StatusCode)
	}

	out := &pb.SetContainerDeletePolicyResponse{}
	if err := protojson.Unmarshal(bodyBytes, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// SetContainerToolAccess records the tool-access level for platform-started
// runs on a container via the REST shim (#2944); BOX_TOOL_ACCESS_UNSPECIFIED
// clears it. A 404 (a daemon too old to expose /v1/containers/{name}/tool-access)
// is mapped to gRPC codes.Unimplemented so callers see the same soft-path across
// both transports.
func (c *HTTPClient) SetContainerToolAccess(username string, level pb.BoxToolAccess) (*pb.SetContainerToolAccessResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/tool-access", url.PathEscape(username))
	// The enum goes by its proto JSON name so grpc-gateway decodes it into the
	// BoxToolAccess enum.
	body, err := json.Marshal(setContainerToolAccessRequest{ToolAccess: level.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("set tool access: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, status.Errorf(codes.Unimplemented, "server does not expose SetContainerToolAccess (HTTP 404)")
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("set tool access: status %d", resp.StatusCode)
	}

	out := &pb.SetContainerToolAccessResponse{}
	if err := protojson.Unmarshal(bodyBytes, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// SetContainerAttribution merges labels onto an existing container via the REST
// shim (cloud #746) — the daemon-side primitive the hosted control plane's
// adopt flow (cloud #539) uses to stamp attribution on a pre-existing box. A
// 404 (daemon too old to expose the endpoint) maps to codes.Unimplemented, so a
// caller can soft-path it the same way as SetContainerDeletePolicy.
func (c *HTTPClient) SetContainerAttribution(username string, labels map[string]string) (*pb.SetContainerAttributionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/attribution", url.PathEscape(username))
	body, err := json.Marshal(setContainerAttributionRequest{Labels: labels})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("set attribution: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, status.Errorf(codes.Unimplemented, "server does not expose SetContainerAttribution (HTTP 404)")
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("set attribution: status %d", resp.StatusCode)
	}

	out := &pb.SetContainerAttributionResponse{}
	if err := protojson.Unmarshal(bodyBytes, out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// StartContainer starts a stopped container via HTTP. When
// waitForReady is true the daemon blocks until the container's
// primary TCP port accepts (or readyTimeoutSeconds elapses).
func (c *HTTPClient) StartContainer(username string, waitForReady bool, readyTimeoutSeconds int32) (*pb.StartContainerResponse, error) {
	timeout := 60 * time.Second
	if waitForReady && readyTimeoutSeconds > 0 {
		timeout = time.Duration(readyTimeoutSeconds+10) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	path := fmt.Sprintf("/v1/containers/%s/start", url.PathEscape(username))
	body, err := json.Marshal(startContainerRequest{
		WaitForReady:        waitForReady,
		ReadyTimeoutSeconds: readyTimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}
	defer drainClose(resp)

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("start container: status %d", resp.StatusCode)
	}
