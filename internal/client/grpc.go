package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/footprintai/containarium/internal/mtls"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// GRPCClient wraps a gRPC connection to the containarium daemon
type GRPCClient struct {
	conn                *grpc.ClientConn
	client              pb.ContainerServiceClient
	appClient           pb.AppServiceClient
	networkClient       pb.NetworkServiceClient
	networkPolicyClient pb.NetworkPolicyServiceClient
	recipeClient        pb.RecipeServiceClient
	backupClient        pb.BackupServiceClient
	volumeClient        pb.VolumeServiceClient
	kmsClient           pb.KmsServiceClient
	agentClient         pb.AgentSkillServiceClient
	crewClient          pb.CrewServiceClient
	clusterClient       pb.ClusterServiceClient
	auditClient         pb.AuditServiceClient // #2415
	// proofPackClient verifies a sealed Proof Pack (#3064).
	proofPackClient pb.ProofPackServiceClient
	sandboxClient   pb.SandboxServiceClient
	anonClient      pb.AnonymousBoxServiceClient
	trackerClient   pb.TrackerServiceClient
	// modelGatewayClient is the model gateway's admin + mint surface (#1726).
	modelGatewayClient pb.ModelGatewayServiceClient
	// guardrailPolicyClient is the server-side guardrail policy (#2368).
	guardrailPolicyClient pb.GuardrailPolicyServiceClient
	// retentionClient is the retention policy and legal holds (#2875).
	retentionClient pb.RetentionServiceClient
}

// NewGRPCClient creates a new gRPC client
func NewGRPCClient(serverAddr string, certsDir string, insecureConn bool) (*GRPCClient, error) {
	if serverAddr == "" {
		return nil, ErrNoServerConfigured
	}

	var opts []grpc.DialOption

	if insecureConn {
		// Insecure connection (not recommended)
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// Use mTLS
		if certsDir == "" {
			// Default to ~/.config/containarium/certs
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			certsDir = filepath.Join(homeDir, ".config", "containarium", "certs")
		}

		// Load certificates
		paths := mtls.CertPaths{
			CACert:     filepath.Join(certsDir, "ca.crt"),
			ClientCert: filepath.Join(certsDir, "client.crt"),
			ClientKey:  filepath.Join(certsDir, "client.key"),
		}

		// Check if certificates exist
		if _, err := os.Stat(paths.CACert); os.IsNotExist(err) {
			return nil, fmt.Errorf("CA certificate not found at %s\nPlease download certificates from the server:\n  gcloud compute scp SERVER:/etc/containarium/certs/ca.crt %s\n  gcloud compute scp SERVER:/etc/containarium/certs/client.crt %s\n  gcloud compute scp SERVER:/etc/containarium/certs/client.key %s",
				paths.CACert, paths.CACert, paths.ClientCert, paths.ClientKey)
		}

		// Load client dial options
		dialOpts, err := mtls.LoadClientDialOptions(paths, serverAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, dialOpts...)
	}

	// Connect to server
	conn, err := grpc.NewClient(serverAddr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial server: %w", err)
	}

	// Create clients
	client := pb.NewContainerServiceClient(conn)
	appClient := pb.NewAppServiceClient(conn)
	networkClient := pb.NewNetworkServiceClient(conn)
	networkPolicyClient := pb.NewNetworkPolicyServiceClient(conn)
	recipeClient := pb.NewRecipeServiceClient(conn)
	backupClient := pb.NewBackupServiceClient(conn)
	volumeClient := pb.NewVolumeServiceClient(conn)
	kmsClient := pb.NewKmsServiceClient(conn)
	agentClient := pb.NewAgentSkillServiceClient(conn)
	crewClient := pb.NewCrewServiceClient(conn)
	clusterClient := pb.NewClusterServiceClient(conn)
	sandboxClient := pb.NewSandboxServiceClient(conn)
	anonClient := pb.NewAnonymousBoxServiceClient(conn)
	trackerClient := pb.NewTrackerServiceClient(conn)
	modelGatewayClient := pb.NewModelGatewayServiceClient(conn)

	return &GRPCClient{
		conn:                conn,
		client:              client,
		appClient:           appClient,
		networkClient:       networkClient,
		networkPolicyClient: networkPolicyClient,
		recipeClient:        recipeClient,
		backupClient:        backupClient,
		volumeClient:        volumeClient,
		kmsClient:           kmsClient,
		agentClient:         agentClient,
		crewClient:          crewClient,
		clusterClient:       clusterClient,
		auditClient:         pb.NewAuditServiceClient(conn),
		proofPackClient:     pb.NewProofPackServiceClient(conn),
		sandboxClient:       sandboxClient,
		anonClient:          anonClient,
		trackerClient:       trackerClient,
		modelGatewayClient:  modelGatewayClient,

		guardrailPolicyClient: pb.NewGuardrailPolicyServiceClient(conn),
		retentionClient:       pb.NewRetentionServiceClient(conn),
	}, nil
}

// Close closes the gRPC connection
func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// Conn returns the underlying *grpc.ClientConn for callers that need
// to instantiate typed clients beyond the ones this wrapper pre-builds
// (container/app/network). Useful for adding a new service without
// also adding pre-built helper methods here for every RPC.
//
// Callers should NOT close the returned conn — its lifetime is owned
// by this GRPCClient and is freed in Close().
func (c *GRPCClient) Conn() *grpc.ClientConn {
	return c.conn
}

// ListContainers lists all containers via gRPC
func (c *GRPCClient) ListContainers() ([]incus.ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.ListContainers(ctx, &pb.ListContainersRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Convert protobuf response to incus.ContainerInfo
	var containers []incus.ContainerInfo
	for _, container := range resp.Containers {
		info := incus.ContainerInfo{
			Name:                container.Name,
			Username:            container.Username,
			SSHHost:             container.SshHost,
			State:               container.State.String(),
			Tenant:              container.NetworkTenant,
			NetworkTenantSource: networkTenantSourceFromProto(container.NetworkTenantSource),
			Labels:              container.Labels,
			InstanceType:        ostype.InstanceTypeFromIsolation(container.Isolation),
		}
		applyProtoLifecycle(&info, container)

		// Get IP address from network info
		if container.Network != nil {
			info.IPAddress = container.Network.IpAddress
		}

		// Get resource limits
		if container.Resources != nil {
			info.CPU = container.Resources.Cpu
			info.Memory = container.Resources.Memory
		}

		// Convert Unix timestamp to time.Time
		if container.CreatedAt > 0 {
			info.CreatedAt = time.Unix(container.CreatedAt, 0)
		}

		containers = append(containers, info)
	}

	return containers, nil
}

// CreateContainer creates a container via gRPC
func (c *GRPCClient) CreateContainer(username, image, cpu, memory, disk string, sshKeys []string, enablePodman bool, stack string, gpus []string, osType pb.OSType, isolation pb.IsolationType, monitoring bool, pool, backendID string, git GitSourceOpts, ttlSeconds int64, idleStopMinutes int32, disableAutoSleep bool, deleteAfterStoppedSeconds int64, storageClass string, enc EncryptionOpts, memoryRequest, cpuRequest, region string, labels map[string]string, networkTenant ...string) (*incus.ContainerInfo, error) {
	if len(networkTenant) > 0 && networkTenant[0] != "" {
		caps, err := c.GetNetworkTenantCapabilities()
		if err != nil {
			return nil, fmt.Errorf("native Tenant capability check: %w", err)
		}
		if err := requireNativeTenantCapability(caps); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute) // Container creation can take time (includes ultra-aggressive retry logic for google_guest_agent)
	defer cancel()
	req := NewCreateContainerRequest(username, image, cpu, memory, disk, sshKeys, enablePodman, stack, gpus, osType, isolation, monitoring, pool, backendID, git, ttlSeconds, idleStopMinutes, disableAutoSleep, deleteAfterStoppedSeconds, storageClass, enc, memoryRequest, cpuRequest, region, labels, networkTenant...)
	resp, err := c.client.CreateContainer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Convert protobuf Container to incus.ContainerInfo
	container := resp.Container
	info := &incus.ContainerInfo{
		Name:                container.Name,
		Username:            container.Username,
		SSHHost:             container.SshHost,
		State:               container.State.String(),
		InstanceType:        ostype.InstanceTypeFromIsolation(container.Isolation),
		Tenant:              container.NetworkTenant,
		NetworkTenantSource: networkTenantSourceFromProto(container.NetworkTenantSource),
		Labels:              container.Labels, // what the daemon stored, incl. --labels (#2358)
	}

	if container.Network != nil {
		info.IPAddress = container.Network.IpAddress
	}

	if container.Resources != nil {
		info.CPU = container.Resources.Cpu
		info.Memory = container.Resources.Memory
	}

	info.GPU = container.GpuDevice
	info.GPUs = container.GpuDevices

	if container.CreatedAt > 0 {
		info.CreatedAt = time.Unix(container.CreatedAt, 0)
	}

	return info, nil
}

// ValidateCreate asks the daemon to run every pre-flight check for req and stop
// before provisioning anything (#2925). nil means the daemon would accept the
// create; the error is the daemon's own rejection otherwise. `create --force`
// calls it before deleting the existing box.
//
// A daemon that predates dry_run ignores the field and answers a real create,
// so only a response that echoes dry_run counts. Anything else is an error:
// treating it as "validated" would delete the box on an unchecked request.
func (c *GRPCClient) ValidateCreate(req *pb.CreateContainerRequest) error {
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
	resp, err := c.client.CreateContainer(ctx, dry)
	if err != nil {
		return fmt.Errorf("pre-flight check failed: %w", err)
	}
	if !resp.GetDryRun() {
		return errDryRunNotHonoured
	}
	return nil
}

// errDryRunNotHonoured is returned when a daemon answered a dry-run create
// with a normal response: it predates the field.
var errDryRunNotHonoured = errors.New("the daemon did not honour the dry run (it predates dry_run support); refusing to treat the request as validated")

// NewCreateContainerRequest builds the wire request for CreateContainer;
// pure, so what the CLI flags turn into is unit-testable (#2358 — labels
// used to be dropped between the CLI and this request).
func NewCreateContainerRequest(username, image, cpu, memory, disk string, sshKeys []string, enablePodman bool, stack string, gpus []string, osType pb.OSType, isolation pb.IsolationType, monitoring bool, pool, backendID string, git GitSourceOpts, ttlSeconds int64, idleStopMinutes int32, disableAutoSleep bool, deleteAfterStoppedSeconds int64, storageClass string, enc EncryptionOpts, memoryRequest, cpuRequest, region string, labels map[string]string, networkTenant ...string) *pb.CreateContainerRequest {
	req := &pb.CreateContainerRequest{
		Username: username,
		Resources: &pb.ResourceLimits{
			Cpu:           cpu,
			Memory:        memory,
			Disk:          disk,
			StorageClass:  storageClass,
			MemoryRequest: memoryRequest,
			CpuRequest:    cpuRequest,
		},
		SshKeys:                   sshKeys,
		Image:                     image,
		EnablePodman:              enablePodman,
		Stack:                     stack,
		Gpus:                      gpus,
		OsType:                    osType,
		Isolation:                 isolation,
		Monitoring:                monitoring,
		Pool:                      pool,
		BackendId:                 backendID,
		Encrypted:                 enc.Encrypted,
		TenantId:                  enc.TenantID,
		GitSource:                 git.Source,
		GitRef:                    git.Ref,
		GitCredential:             git.Credential,
		WorkspacePath:             git.WorkspacePath,
		TtlSeconds:                ttlSeconds,
		IdleStopMinutes:           idleStopMinutes,
		DisableAutoSleep:          disableAutoSleep,
		DeleteAfterStoppedSeconds: deleteAfterStoppedSeconds,
		Region:                    region,
	}

	if len(networkTenant) > 0 {
		req.NetworkTenant = networkTenant[0]
	}
	if len(labels) > 0 {
		req.Labels = labels
	}
	return req
}

// ToggleMonitoring enables / disables OTel app telemetry on an
// existing container. Returns the new monitoring_enabled state and
// a human-readable summary of what changed.
func (c *GRPCClient) ToggleMonitoring(username string, enabled bool) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := &pb.ToggleMonitoringRequest{
		Username: username,
		Enabled:  enabled,
	}
	resp, err := c.client.ToggleMonitoring(ctx, req)
	if err != nil {
		return "", false, fmt.Errorf("failed to toggle monitoring: %w", err)
	}
	return resp.Message, resp.MonitoringEnabled, nil
}

// SetMetricsExport enables or disables opt-in export of host/container
// infra metrics to the host cloud's native monitoring (#1069). Enabling
// requires a supported provider and passes the daemon's synchronous
// credential probe (e.g. GCP ADC resolution); a failed probe or an
// unsupported provider (AWS, UNSPECIFIED) returns the daemon's error
// unwrapped so the CLI can render the gRPC code (FailedPrecondition /
// InvalidArgument / Unimplemented) directly.
func (c *GRPCClient) SetMetricsExport(enabled bool, provider pb.CloudMetricsProvider, groups []pb.CloudMetricsGroup) (*pb.SetMetricsExportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return c.client.SetMetricsExport(ctx, &pb.SetMetricsExportRequest{
		Enabled:  enabled,
		Provider: provider,
		Groups:   groups,
	})
}

// GetMetricsExport returns the current cloud-native metrics export
// configuration and last-known health (#1069).
func (c *GRPCClient) GetMetricsExport() (*pb.GetMetricsExportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return c.client.GetMetricsExport(ctx, &pb.GetMetricsExportRequest{})
}

// ToggleAutoSleep writes the per-container auto-sleep opt-in metadata.
// idleThresholdMinutes is ignored when enabled is false; 0 means
// "leave the existing key or fall back to the 15-minute default".
func (c *GRPCClient) ToggleAutoSleep(username string, enabled bool, idleThresholdMinutes int32) (*pb.ToggleAutoSleepResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &pb.ToggleAutoSleepRequest{
		Username:             username,
		Enabled:              enabled,
		IdleThresholdMinutes: idleThresholdMinutes,
	}
	resp, err := c.client.ToggleAutoSleep(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to toggle auto-sleep: %w", err)
	}
	return resp, nil
}

// SetContainerTTL schedules (durationSeconds > 0) or clears (== 0) a
// container's auto-delete TTL. The daemon stamps
// user.containarium.ttl_expires_at, which the ttlsweeper consumes to
// force-delete the box when it elapses. Used by the containarium-run GitHub
// Action's keep-on-failure path so a kept debug box self-reaps (#264 TTL-404).
//
// The error is returned UNWRAPPED so a daemon too old to implement the RPC
// surfaces as gRPC codes.Unimplemented, which the `containarium ttl` CLI
// treats as a soft no-op rather than a hard failure.
func (c *GRPCClient) SetContainerTTL(username string, durationSeconds int64) (*pb.SetContainerTTLResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return c.client.SetContainerTTL(ctx, &pb.SetContainerTTLRequest{
		Name:            username,
		DurationSeconds: durationSeconds,
	})
}

// SetContainerDeletePolicy protects (DELETE_POLICY_PROTECTED) or unprotects
// (DELETE_POLICY_UNSPECIFIED) a container from the daemon's automated/bulk
// deletion paths — the ttlsweeper auto-reap and `containarium prune` (#284).
//
// The error is returned UNWRAPPED so a daemon too old to implement the RPC
// surfaces as gRPC codes.Unimplemented, which the `containarium protect` CLI
// treats as a soft no-op rather than a hard failure.
func (c *GRPCClient) SetContainerDeletePolicy(username string, policy pb.DeletePolicy) (*pb.SetContainerDeletePolicyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return c.client.SetContainerDeletePolicy(ctx, &pb.SetContainerDeletePolicyRequest{
		Name:         username,
		DeletePolicy: policy,
	})
}

// SetContainerToolAccess records the tool-access level for platform-started
// runs on a container (#2944); BOX_TOOL_ACCESS_UNSPECIFIED clears it. The error
// is returned UNWRAPPED so a daemon too old to implement the RPC surfaces as
// gRPC codes.Unimplemented.
func (c *GRPCClient) SetContainerToolAccess(username string, level pb.BoxToolAccess) (*pb.SetContainerToolAccessResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return c.client.SetContainerToolAccess(ctx, &pb.SetContainerToolAccessRequest{
		Name:       username,
		ToolAccess: level,
	})
}

// SetContainerAttribution merges labels onto an existing container (cloud #746)
// — the daemon-side primitive the hosted control plane's adopt flow (cloud
// #539) uses to stamp attribution on a pre-existing box. Cloud→daemon plumbing;
// no CLI verb.
func (c *GRPCClient) SetContainerAttribution(username string, labels map[string]string) (*pb.SetContainerAttributionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return c.client.SetContainerAttribution(ctx, &pb.SetContainerAttributionRequest{
		Name:   username,
		Labels: labels,
	})
}

// StartContainer starts a stopped container via gRPC. When
// waitForReady is true the server blocks until the container's
// primary TCP port accepts or readyTimeoutSeconds elapses (0 falls
// back to the server-side 30s default).
func (c *GRPCClient) StartContainer(username string, waitForReady bool, readyTimeoutSeconds int32) (*pb.StartContainerResponse, error) {
	timeout := 60 * time.Second
	if waitForReady && readyTimeoutSeconds > 0 {
		timeout = time.Duration(readyTimeoutSeconds+10) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := c.client.StartContainer(ctx, &pb.StartContainerRequest{
		Username:            username,
		WaitForReady:        waitForReady,
		ReadyTimeoutSeconds: readyTimeoutSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}
	return resp, nil
}

// StopContainer stops a running container via gRPC.
func (c *GRPCClient) StopContainer(username string, force bool) (*pb.StopContainerResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := c.client.StopContainer(ctx, &pb.StopContainerRequest{
		Username: username,
		Force:    force,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stop container: %w", err)
	}
	return resp, nil
}

// DeleteContainer deletes a container via gRPC
func (c *GRPCClient) DeleteContainer(username string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &pb.DeleteContainerRequest{
		Username: username,
		Force:    force,
	}

	_, err := c.client.DeleteContainer(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete container: %w", err)
	}

	return nil
}

// SetSecret creates or updates a tenant secret via gRPC. Idempotent —
// repeated calls with the same (username, name) bump the version.
// `delivery` is "" (server normalizes to env), "env", or "file"
// (Phase 4.3 — Phase A lands the field).
func (c *GRPCClient) SetSecret(username, name, value, delivery string) (*pb.SecretMetadata, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.SetSecret(ctx, &pb.SetSecretRequest{
		Username: username, Name: name, Value: value, Delivery: delivery,
	})
	if err != nil {
		return nil, "", fmt.Errorf("set secret: %w", err)
	}
	return resp.Secret, resp.Message, nil
}

// GetSecret reads a single secret's plaintext value via gRPC.
func (c *GRPCClient) GetSecret(username, name string) (*pb.SecretMetadata, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.GetSecret(ctx, &pb.GetSecretRequest{
		Username: username, Name: name,
	})
	if err != nil {
		return nil, "", fmt.Errorf("get secret: %w", err)
	}
	return resp.Secret, resp.Value, nil
}

// ListSecrets returns metadata for every secret owned by the tenant.
func (c *GRPCClient) ListSecrets(username string) ([]*pb.SecretMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.ListSecrets(ctx, &pb.ListSecretsRequest{Username: username})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	return resp.Secrets, nil
}

// DeleteSecret removes a tenant secret via gRPC.
func (c *GRPCClient) DeleteSecret(username, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.DeleteSecret(ctx, &pb.DeleteSecretRequest{
		Username: username, Name: name,
	})
	if err != nil {
		return "", fmt.Errorf("delete secret: %w", err)
	}
	return resp.Message, nil
}

// RefreshSecrets re-stamps the LXC's env from the current secret DB
// state for the tenant. Running processes keep their old env; new
// execs see the refreshed values.
func (c *GRPCClient) RefreshSecrets(username string) (string, int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.RefreshSecrets(ctx, &pb.RefreshSecretsRequest{Username: username})
	if err != nil {
		return "", 0, fmt.Errorf("refresh secrets: %w", err)
	}
	return resp.Message, resp.Stamped, nil
}

// SetTenantKMSKey sets (or, with an empty keyResourceName, clears) a
// tenant's per-tenant KMS key via gRPC (#1630). Admin role + explicit
// secrets:write scope required on the token — see
// ContainerServer.SetTenantKMSKey's doc comment.
func (c *GRPCClient) SetTenantKMSKey(username, keyResourceName string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.SetTenantKMSKey(ctx, &pb.SetTenantKMSKeyRequest{
		Username: username, KekResourceName: keyResourceName,
	})
	if err != nil {
		return "", false, fmt.Errorf("set tenant kms key: %w", err)
	}
	return resp.Message, resp.HasTenantKey, nil
}

// ResizeContainer changes a container's CPU / memory / disk via gRPC.
// Empty string for any field means "no change". Disk can only grow —
// the server rejects shrinks. memoryRequest/cpuRequest are the K8s
// scheduler-request counterparts to memory/cpu (K8s backend only, ignored on
// LXC); empty means the existing request is left alone, even when the
// matching limit changes.
func (c *GRPCClient) ResizeContainer(username, cpu, memory, disk, memoryRequest, cpuRequest string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := &pb.ResizeContainerRequest{
		Username:      username,
		Cpu:           cpu,
		Memory:        memory,
		Disk:          disk,
		MemoryRequest: memoryRequest,
		CpuRequest:    cpuRequest,
	}
	resp, err := c.client.ResizeContainer(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to resize container: %w", err)
	}
	return resp.Message, nil
}

// GetContainer gets information about a specific container via gRPC
func (c *GRPCClient) GetContainer(username string) (*incus.ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetContainerRequest{
		Username: username,
	}

	resp, err := c.client.GetContainer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	// Convert protobuf Container to incus.ContainerInfo
	container := resp.Container
	info := &incus.ContainerInfo{
		Name:                container.Name,
		Username:            container.Username,
		State:               container.State.String(),
		Tenant:              container.NetworkTenant,
		NetworkTenantSource: networkTenantSourceFromProto(container.NetworkTenantSource),
		Labels:              container.Labels,
	}
	applyProtoLifecycle(info, container)

	if container.Network != nil {
		info.IPAddress = container.Network.IpAddress
	}

	if container.Resources != nil {
		info.CPU = container.Resources.Cpu
		info.Memory = container.Resources.Memory
	}

	info.GPU = container.GpuDevice
	info.GPUs = container.GpuDevices

	if container.CreatedAt > 0 {
		info.CreatedAt = time.Unix(container.CreatedAt, 0)
	}

	return info, nil
}

// AddCollaborator adds a collaborator to a container via gRPC.
func (c *GRPCClient) AddCollaborator(ownerUsername, collaboratorUsername string, sshPublicKeys []string, grantSudo, grantContainerRuntime bool) (*pb.AddCollaboratorResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.AddCollaborator(ctx, &pb.AddCollaboratorRequest{
		OwnerUsername:         ownerUsername,
		CollaboratorUsername:  collaboratorUsername,
		SshPublicKeys:         sshPublicKeys,
		GrantSudo:             grantSudo,
		GrantContainerRuntime: grantContainerRuntime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add collaborator: %w", err)
	}
	return resp, nil
}

// RemoveCollaborator removes a collaborator from a container via gRPC.
func (c *GRPCClient) RemoveCollaborator(ownerUsername, collaboratorUsername string) (*pb.RemoveCollaboratorResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.RemoveCollaborator(ctx, &pb.RemoveCollaboratorRequest{
		OwnerUsername:        ownerUsername,
		CollaboratorUsername: collaboratorUsername,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to remove collaborator: %w", err)
	}
	return resp, nil
}

// ListCollaborators lists collaborators for a container via gRPC.
func (c *GRPCClient) ListCollaborators(ownerUsername string) (*pb.ListCollaboratorsResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.ListCollaborators(ctx, &pb.ListCollaboratorsRequest{
		OwnerUsername: ownerUsername,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list collaborators: %w", err)
	}
	return resp, nil
}

// ListBoxRuns lists a box's code-run records via gRPC (#2123).
func (c *GRPCClient) ListBoxRuns(username string) ([]*pb.BoxRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.client.ListBoxRuns(ctx, &pb.ListBoxRunsRequest{Username: username})
	if err != nil {
		return nil, err
	}
	return resp.GetRuns(), nil
}

// GetBoxRunUserStatus reports, read-only, whether the unprivileged run user on a
// box has a usable agent sign-in: the probe StartBoxRun refuses on, without
// queueing a run (#3274). The error is returned UNWRAPPED so a daemon too old to
// implement the RPC surfaces as gRPC codes.Unimplemented.
func (c *GRPCClient) GetBoxRunUserStatus(username string) (*pb.GetBoxRunUserStatusResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.GetBoxRunUserStatus(ctx, &pb.GetBoxRunUserStatusRequest{Username: username})
}

// sweepBoxRunCheckoutsTimeout bounds a sweep call: removing large checkouts
// can take far longer than a read.
const sweepBoxRunCheckoutsTimeout = 10 * time.Minute

// SweepBoxRunCheckouts runs the per-run checkout sweep once on a box (#3296);
// dry_run lists what it would remove and removes nothing. The error is
// returned UNWRAPPED so an older daemon surfaces as codes.Unimplemented.
func (c *GRPCClient) SweepBoxRunCheckouts(req *pb.SweepBoxRunCheckoutsRequest) (*pb.SweepBoxRunCheckoutsResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sweepBoxRunCheckoutsTimeout)
	defer cancel()
	return c.client.SweepBoxRunCheckouts(ctx, req)
}

// TailBoxRunLog reads one window of a box run's log via gRPC (#2123). The
// deadline covers the server's follow_seconds wait.
func (c *GRPCClient) TailBoxRunLog(req *pb.TailBoxRunLogRequest) (*pb.TailBoxRunLogResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.TailBoxRunLog(ctx, req)
}

// GetRunApprovalKey returns a box's approval public key via gRPC (#2184).
func (c *GRPCClient) GetRunApprovalKey(username string) (*pb.GetRunApprovalKeyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.GetRunApprovalKey(ctx, &pb.GetRunApprovalKeyRequest{Username: username})
}

// ListRunInputs lists a box run's open permission requests via gRPC (#2184).
func (c *GRPCClient) ListRunInputs(req *pb.ListRunInputsRequest) (*pb.ListRunInputsResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.ListRunInputs(ctx, req)
}

// AnswerRunInput answers one permission request via gRPC (#2184). The
// deadline covers the server's wait for the box's response.
func (c *GRPCClient) AnswerRunInput(req *pb.AnswerRunInputRequest) (*pb.AnswerRunInputResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.AnswerRunInput(ctx, req)
}

// DebugContainer returns a diagnostic report for a container's SSH path.
func (c *GRPCClient) DebugContainer(username string) (*pb.DebugContainerResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.DebugContainer(ctx, &pb.DebugContainerRequest{Username: username})
	if err != nil {
		return nil, fmt.Errorf("failed to debug container: %w", err)
	}
	return resp, nil
}

// GetConsoleLog returns a VM instance's boot-time console ring-buffer log.
func (c *GRPCClient) GetConsoleLog(username string) (*pb.GetConsoleLogResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.GetConsoleLog(ctx, &pb.GetConsoleLogRequest{Username: username})
	if err != nil {
		return nil, fmt.Errorf("failed to get console log: %w", err)
	}
	return resp, nil
}

// InstallStack installs a stack or base script on a running container via gRPC
func (c *GRPCClient) InstallStack(username, stackID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req := &pb.InstallStackRequest{
		Username: username,
		StackId:  stackID,
	}

	_, err := c.client.InstallStack(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to install stack: %w", err)
	}

	return nil
}

// GetSystemInfo gets system information via gRPC
func (c *GRPCClient) GetSystemInfo() (*incus.ServerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.GetSystemInfo(ctx, &pb.GetSystemInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	// Convert to incus.ServerInfo
	info := &incus.ServerInfo{
		Version:              resp.Info.IncusVersion,
		KernelVersion:        resp.Info.KernelVersion,
		TotalMemoryBytes:     resp.Info.TotalMemoryBytes,
		AvailableMemoryBytes: resp.Info.AvailableMemoryBytes,
		CPUBudget: cpuBudgetFromWire(resp.Info.TotalCpus, resp.Info.CommittedCpuCores,
			resp.Info.CoreCommittedCpuCores, resp.Info.CpuAdmissionMode, resp.Info.CpuOvercommitFactor),
		StoragePools: storagePoolsFromWire(resp.Info.StoragePools),
	}

	return info, nil
}

// ManagedBackupKeyConfigured reports whether the daemon has a managed backup
// key, from SystemInfo.managed_backup_key_configured (#3213).
func (c *GRPCClient) ManagedBackupKeyConfigured() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.GetSystemInfo(ctx, &pb.GetSystemInfoRequest{})
	if err != nil {
		return false, fmt.Errorf("failed to get system info: %w", err)
	}
	return resp.GetInfo().GetManagedBackupKeyConfigured(), nil
}

// ============================================
// App Service Methods
// ============================================

// DeployApp deploys an application via gRPC
func (c *GRPCClient) DeployApp(username, appName string, sourceTarball []byte, port int32, envVars map[string]string, subdomain string) (*pb.App, *pb.DetectedLanguage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // Build can take time
	defer cancel()

	req := &pb.DeployAppRequest{
		Username:      username,
		AppName:       appName,
		SourceTarball: sourceTarball,
		Port:          port,
		EnvVars:       envVars,
		Subdomain:     subdomain,
	}

	resp, err := c.appClient.DeployApp(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to deploy app: %w", err)
	}

	return resp.App, resp.DetectedLanguage, nil
}

// ListRecipes lists all built-in recipes via gRPC.
func (c *GRPCClient) ListRecipes() ([]*pb.Recipe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.recipeClient.ListRecipes(ctx, &pb.ListRecipesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list recipes: %w", err)
	}
	return resp.Recipes, nil
}

// GetRecipe fetches a single recipe definition via gRPC.
func (c *GRPCClient) GetRecipe(id string) (*pb.Recipe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.recipeClient.GetRecipe(ctx, &pb.GetRecipeRequest{Id: id})
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe: %w", err)
	}
	return resp.Recipe, nil
}

// GetWorkspaceAccess fetches the zero-click bootstrap URL for an
// agent-workspace box via gRPC.
func (c *GRPCClient) GetWorkspaceAccess(name string) (*pb.GetWorkspaceAccessResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.recipeClient.GetWorkspaceAccess(ctx, &pb.GetWorkspaceAccessRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace access: %w", err)
	}
	return resp, nil
}

// DeployRecipe provisions a new dedicated container from a recipe via gRPC.
// guardrailInput is the dataset and attestation for a guardrail-gated recipe
// (#2368); nil for an ungated one.
func (c *GRPCClient) DeployRecipe(recipeID, name, gpu, backendID, pool string, params map[string]string, guardrailInput *pb.GuardrailGateInput) (*pb.DeployRecipeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute) // image + model pulls can take time
	defer cancel()

	req := &pb.DeployRecipeRequest{
		RecipeId:       recipeID,
		Name:           name,
		Gpu:            gpu,
		BackendId:      backendID,
		Pool:           pool,
		Parameters:     params,
		GuardrailInput: guardrailInput,
	}
	resp, err := c.recipeClient.DeployRecipe(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy recipe: %w", err)
	}
	return resp, nil
}

// ListAgentEngines reports, for each agent engine, whether a run naming it
// would be refused right now (#2223) — via gRPC.
func (c *GRPCClient) ListAgentEngines() (*pb.ListAgentEnginesResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.agentClient.ListAgentEngines(ctx, &pb.ListAgentEnginesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list agent engines: %w", err)
	}
	return resp, nil
}

// ListAgentSkills lists all built-in agent skills via gRPC.
func (c *GRPCClient) ListAgentSkills() ([]*pb.AgentSkill, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.agentClient.ListAgentSkills(ctx, &pb.ListAgentSkillsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list agent skills: %w", err)
	}
	return resp.Skills, nil
}

// GetAgentSkill fetches a single agent skill definition via gRPC.
func (c *GRPCClient) GetAgentSkill(id string) (*pb.AgentSkill, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.agentClient.GetAgentSkill(ctx, &pb.GetAgentSkillRequest{Id: id})
	if err != nil {
		return nil, fmt.Errorf("failed to get agent skill: %w", err)
	}
	return resp.Skill, nil
}

// ProvisionSkillBox creates or reuses a skill's deterministic box
// (agent-<skill_id>) via gRPC, without running it: no token minted, nothing
// seeded, no model call (#2272). Lets a human get the box provisioned ahead
// of any real run, so they can sign in to its coding agent before any
// inference credential exists.
func (c *GRPCClient) ProvisionSkillBox(skillID, backendID, pool string) (*pb.ProvisionSkillBoxResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // box deploy can take time
	defer cancel()

	resp, err := c.agentClient.ProvisionSkillBox(ctx, &pb.ProvisionSkillBoxRequest{
		SkillId:   skillID,
		BackendId: backendID,
		Pool:      pool,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to provision skill box: %w", err)
	}
	return resp, nil
}

// GetSkillBoxCredentialStatus reports, via gRPC, whether a skill's
// already-provisioned box has a credential its configured coding engine
// would use — the source NAME only, never a value (#2272, #2030 posture).
func (c *GRPCClient) GetSkillBoxCredentialStatus(skillID string) (*pb.GetSkillBoxCredentialStatusResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.agentClient.GetSkillBoxCredentialStatus(ctx, &pb.GetSkillBoxCredentialStatusRequest{SkillId: skillID})
	if err != nil {
		return nil, fmt.Errorf("failed to get skill box credential status: %w", err)
	}
	return resp, nil
}

// RunAgentSkill provisions a skill's box, mints a scoped token, runs one task,
// and returns the box via gRPC. gitSource/gitRef/gitCredential (#1859) fetch a
// repo into the run's workspace before the agent starts; empty gitSource
// means no fetch. trackerConnection (#2042) binds the run to one of the
// caller's tracker connections; empty means no binding. engine (#2228) wins
// over the skill's own manifest engine; AGENT_ENGINE_UNSPECIFIED means "use
// the manifest", unchanged pre-#2228 behavior.
func (c *GRPCClient) RunAgentSkill(skillID, backendID, pool, inputJSON, gitSource, gitRef, gitCredential, trackerConnection string, engine pb.AgentEngine) (*pb.RunAgentSkillResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute) // box provisioning can take time
	defer cancel()

	req := &pb.RunAgentSkillRequest{
		SkillId:           skillID,
		BackendId:         backendID,
		Pool:              pool,
		InputJson:         inputJSON,
		GitSource:         gitSource,
		GitRef:            gitRef,
		GitCredential:     gitCredential,
		TrackerConnection: trackerConnection,
		Engine:            engine,
	}
	resp, err := c.agentClient.RunAgentSkill(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to run agent skill: %w", err)
	}
	return resp, nil
}

// EnqueueAgentTask places a task on the pull queue for a skill (prototype).
func (c *GRPCClient) EnqueueAgentTask(skillID, inputJSON string) (*pb.EnqueueAgentTaskResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.agentClient.EnqueueAgentTask(ctx, &pb.EnqueueAgentTaskRequest{
		SkillId:   skillID,
		InputJson: inputJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue agent task: %w", err)
	}
	return resp, nil
}

// StartAgentWorker launches a poll-mode worker box for a skill (prototype).
func (c *GRPCClient) StartAgentWorker(skillID, backendID, pool, workerID string) (*pb.StartAgentWorkerResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute) // box provisioning can take time
	defer cancel()

	resp, err := c.agentClient.StartAgentWorker(ctx, &pb.StartAgentWorkerRequest{
		SkillId:   skillID,
		BackendId: backendID,
		Pool:      pool,
		WorkerId:  workerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start agent worker: %w", err)
	}
	return resp, nil
}

// SendAgentTask delegates a task to a running peer agent over A2A via gRPC.
func (c *GRPCClient) SendAgentTask(fromSkillID, toPeerID, inputJSON string) (*pb.AgentArtifact, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := c.agentClient.SendAgentTask(ctx, &pb.SendAgentTaskRequest{
		FromSkillId: fromSkillID,
		ToPeerId:    toPeerID,
		InputJson:   inputJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send agent task: %w", err)
	}
	return resp.Artifact, nil
