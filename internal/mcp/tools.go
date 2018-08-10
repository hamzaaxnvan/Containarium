package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/releasecheck"
	"github.com/footprintai/containarium/internal/runlog"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/core/expose"
	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ephemeralKeyDir returns the directory the MCP server writes ephemeral
// private keys to. Defaults to $HOME/.containarium/keys. Overridable via
// CONTAINARIUM_KEYS_DIR for operators who want a non-default location.
// Returns "" if no usable directory can be determined (HOME unset and no
// override) — the caller should treat that as "skip the file write".
func ephemeralKeyDir() string {
	if d := os.Getenv("CONTAINARIUM_KEYS_DIR"); d != "" {
		return d
	}
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".containarium", "keys")
}

// saveEphemeralPrivateKey writes the freshly-minted private key to disk so
// the operator doesn't have to lift it out of the tool response. Returns
// the path it wrote to on success, or an error describing why it didn't.
// On any error the caller should keep showing the key text in the response
// so the agent at least has the option to save it itself.
func saveEphemeralPrivateKey(username string, key []byte) (string, error) {
	dir := ephemeralKeyDir()
	if dir == "" {
		return "", fmt.Errorf("no key directory available (HOME and CONTAINARIUM_KEYS_DIR both unset)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, username)
	// Write atomically: create at a temp name in the same dir, chmod, then
	// rename. Avoids leaving a half-written 0600 file under the username
	// path if the write is interrupted, and avoids ever leaving the key
	// readable to other users on the filesystem.
	f, err := os.CreateTemp(dir, "."+username+".tmp.*")
	if err != nil {
		return "", fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpPath := f.Name()
	// Best-effort cleanup if anything below fails before the rename.
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("chmod 0600 on %s: %w", tmpPath, err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write key bytes: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	tmpPath = "" // rename consumed it
	return path, nil
}

// Tool represents an MCP tool (function).
//
// Phase 1.7 — RequiredScope names the least-privilege scope
// (`<resource>:<action>`) a JWT must carry to invoke this
// tool. Empty string means "no scope needed" (introspection
// helpers only). The MCP server filters tools/list and
// rejects tools/call when the JWT's scope set doesn't cover
// the requirement; see internal/auth/scopes.go for the
// taxonomy and semantics.
//
// Backwards compat: tokens minted before Phase 1.7 have no
// scopes claim. HasScope treats nil as "no restriction", so
// existing tokens continue to see every tool.
type Tool struct {
	Name          string
	Description   string
	InputSchema   map[string]interface{}
	Handler       ToolHandler
	RequiredScope string
}

// ToolHandler is a function that handles a tool call
type ToolHandler func(client API, args map[string]interface{}) (string, error)

// registerTools registers all available MCP tools
func (s *Server) registerTools() {
	s.tools = []Tool{
		{
			Name: "create_container",
			Description: "Create a new LXC container under a username. Returns the container's " +
				"name, IP address, and resources.\n\n" +
				"SSH key handling: if you OMIT `ssh_keys`, an ephemeral ed25519 keypair is " +
				"generated client-side. The public half is installed on the container; the " +
				"private half is auto-saved to `~/.containarium/keys/<username>` with mode " +
				"0600 by this MCP server itself (no agent action required) and ALSO echoed " +
				"into the tool response as a fallback. If you pass `ssh_keys`, those are " +
				"used as-is and no ephemeral key is generated (useful when reusing an " +
				"operator's existing key for SSH alias convenience).\n\n" +
				"AFTER creation, to operate inside the container (simplest path):\n" +
				"  1. The private key is already on disk at ~/.containarium/keys/<name>.\n" +
				"     (Only act on the key text in the response if the response says auto-\n" +
				"     save failed.)\n" +
				"  2. The tool response includes a ready-to-paste ssh command:\n" +
				"       ssh -i ~/.containarium/keys/<name> \\\n" +
				"           -o IdentitiesOnly=yes -o PreferredAuthentications=publickey \\\n" +
				"           <name>@<sentinel-host>\n" +
				"     Use Bash to run it. No edits to ~/.ssh/config required.\n" +
				"     IdentitiesOnly=yes is REQUIRED — without it, ssh offers every\n" +
				"     identity in ~/.ssh/, each of which sshpiper's failtoban counts\n" +
				"     as a failed attempt. One careless ssh can burn the IP's ban quota.\n" +
				"  3. Inside the container, apt install / write files / start services.\n" +
				"  4. Call `expose_port` to make a container port reachable on a public hostname.\n\n" +
				"Shipping your own code into the container (instead of just apt-installing\n" +
				"things): use one of the dedicated transfer tools rather than scp-ing by\n" +
				"hand. They reuse the SSH path above and handle the failtoban / shell-stack\n" +
				"quirks for you.\n" +
				"  - `push`: git-style. Ships committed history; delta on subsequent calls\n" +
				"    via `git bundle`. Refuses on dirty tree unless `include_wip=true`.\n" +
				"    Use when you've committed locally and want commit-by-commit shipping.\n" +
				"  - `sync`: rsync-style. Mirrors the working directory including .git/,\n" +
				"    uncommitted modifications, untracked files. Delta on subsequent calls\n" +
				"    via content-hash diff. Use when you want the container to reflect\n" +
				"    your local state right now, WIP and all.\n\n" +
				"Diagnostic: if SSH or the workflow fails, call `debug_container` BEFORE\n" +
				"reporting the failure. It surfaces host-side state the agent can't see\n" +
				"(user account presence, shell wrapper, recent sshd journal lines) and\n" +
				"returns a likely_cause + ordered next_actions.\n\n" +
				"Optional convenience (skip if you don't want to touch ~/.ssh/config):\n" +
				"  Call the `sync_ssh_config` MCP tool to generate a self-contained\n" +
				"  ssh_config file and Include line. After that, `ssh <name>` works directly.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username for the container (required)",
					},
					"cpu": map[string]interface{}{
						"type":        "string",
						"description": "CPU limit (e.g., '4' for 4 cores, default: 4)",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "Memory limit (e.g., '4GB', '2048MB', default: 4GB)",
					},
					"disk": map[string]interface{}{
						"type":        "string",
						"description": "Disk limit (e.g., '50GB', '100GB', default: 50GB)",
					},
					"ssh_keys": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "SSH public keys to authorize (optional)",
					},
					"image": map[string]interface{}{
						"type":        "string",
						"description": "Container image (default: images:ubuntu/24.04)",
					},
					"enable_podman": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable Podman support (default: true)",
					},
					"network_tenant": map[string]interface{}{"type": "string", "description": "Administrator-authorized native network Tenant; local Linux LXC only. Independent of encryption tenant_id; enforced NIC ACL before first Start."},
					"disable_auto_sleep": map[string]interface{}{
						"type":        "boolean",
						"description": "Create the box with NO auto-sleep timer (default: false). Only matters on a hosted platform that defaults some orgs (for example seat-billed ones) to a 30-minute idle-stop; a self-hosted daemon never auto-sleeps a new box and ignores it.",
					},
					"gpu": map[string]interface{}{
						"type":        "string",
						"description": "Deprecated single-GPU field; prefer 'gpus'. GPU device ID for passthrough (e.g., '0' for first GPU, PCI address, or empty for none)",
					},
					"gpus": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "GPU devices for passthrough — one entry per GPU (index like '0'/'1' or PCI address). Use for multi-GPU boxes. Supersedes 'gpu' when set.",
					},
					"os_type": map[string]interface{}{
						"type":        "string",
						"description": "Container OS type: 'ubuntu' (default), 'rocky9' (dev/test), 'rhel9' (production). Overrides image when set.",
						"enum":        []string{"", "ubuntu", "rocky9", "rhel9"},
					},
					"isolation": map[string]interface{}{
						"type":        "string",
						"description": "How the box is isolated from the host: 'container' (LXC, shared kernel — the default for Linux) or 'vm' (QEMU/KVM virtual machine with its own kernel; needs a KVM-capable backend, and is the only option for Windows). Mirrors `containarium create --isolation`.",
						"enum":        []string{"", "container", "vm"},
					},
					"monitoring": map[string]interface{}{
						"type":        "boolean",
						"description": "Opt the container into application-emitted OpenTelemetry. When true, the daemon stamps the LXC with OTEL_EXPORTER_OTLP_ENDPOINT (and related env vars) pointing at the platform's core OTel collector, so any OTel SDK inside the container ships telemetry without app-side configuration. Default false. The daemon's own cgroup-level metrics for the container (CPU/mem/disk/net) are independent of this flag and continue for every container regardless.",
					},
					"encrypted": map[string]interface{}{
						"type":        "boolean",
						"description": "Encrypt the container's ZFS dataset with a tenant-scoped key rather than the pool-wide key, so a co-tenant — or host root once the box is stopped — sees ciphertext. Requires the daemon to have a KeyProvider configured: without one the create is REFUSED (FAILED_PRECONDITION) rather than silently producing an unencrypted container. Default false. Mirrors `containarium create --encrypted`.",
					},
					"tenant_id": map[string]interface{}{
						"type":        "string",
						"description": "Tenant that owns the container, scoping its encryption key. A single-tenant daemon accepts only an empty value or 'default' and rejects anything else with INVALID_ARGUMENT.",
					},
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Place the container on any healthy backend tagged with this pool (e.g., 'demo', 'lab', 'prod'). When omitted, the request lands on the primary/local backend. Use list_backends to see available pools. Mutually exclusive with backend_id unless the chosen backend is already in this pool (the daemon validates consistency).",
					},
					"backend_id": map[string]interface{}{
						"type":        "string",
						"description": "Place the container on a specific backend by ID (e.g., 'tunnel-node-a-gpu'). Look up valid IDs via list_backends. Use pool instead when any backend in a pool will do.",
					},
					"region": map[string]interface{}{
						"type":        "string",
						"description": "Place the container in this region on a multi-region hosted control plane (e.g., 'us-east'). Ignored by a standalone/single-region daemon.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleCreateContainer,
		},
		{
			Name: "list_containers",
			Description: "List all containers with name, username, state, IP, and resources. " +
				"Useful as a first step (\"what's already running?\") and after create_container " +
				"to confirm the new container's IP. Read-only — no side effects.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleListContainers,
		},
		{
			Name:        "get_container",
			Description: "Get detailed information about a specific container including metrics",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to get",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleGetContainer,
		},
		{
			Name: "debug_container",
			Description: "Diagnose why SSH to a container is failing. Call this BEFORE " +
				"reporting an SSH failure to the user — it surfaces host-side state " +
				"the agent can't see (whether the Linux user account exists on the " +
				"backend, whether the user's shell file resolves, recent sshd journal " +
				"lines mentioning the user) and returns a likely_cause plus an ordered " +
				"next_actions list.\n\n" +
				"Typical failure modes this catches:\n" +
				"  - Container missing or stopped (start it or recreate)\n" +
				"  - Host user account missing (daemon never created or it was wiped)\n" +
				"  - Host user's shell points at a file that doesn't exist on the\n" +
				"    backend — sshd rejects every login\n" +
				"  - sshd journal lines with a concrete \"User X not allowed because\n" +
				"    …\" reason\n\n" +
				"Returns a JSON object with: containerState, hostUserExists, hostUserShell, " +
				"hostUserShellExists, recentSshdRejections, likelyCause, nextActions, " +
				"sourceRepo, daemonVersion, and sshIngressHost — the advertised public " +
				"SSH entrypoint (sentinel host); EMPTY means NO external SSH entrypoint " +
				"(direct/in-network mode), so next_actions drop the sentinel hints and " +
				"there is no jump host to chase (reach the container by IP or its " +
				"in-network deploy pipeline). When the structured fields are inconclusive, " +
				"use sourceRepo + daemonVersion to fetch the daemon's source and dig " +
				"deeper (grep internal/sentinel/, pkg/core/, etc.). Read-only — no side effects.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to debug",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleDebugContainer,
		},
		{
			Name: "move_container",
			Description: "Migrate a container from this daemon to a peer daemon using " +
				"pre-copy snapshot + delta refresh. The container's hostname, ACME cert, " +
				"persistent disk state, and user accounts all transfer. Process memory " +
				"state is NOT preserved (use stateful=true if CRIU is configured on " +
				"both ends, which is rare in practice).\n\n" +
				"Downtime is sub-second on ZFS/btrfs storage with low write rate; " +
				"potentially minutes on dir-pool or highly active workloads. The route " +
				"store target_ip swap propagates to Caddy within ~5 seconds.\n\n" +
				"Prereqs: the target backend must be visible in /v1/backends, and the " +
				"source's incusd must have the target configured as an `incus remote` " +
				"(remote name == target_backend_id by convention).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to migrate",
					},
					"target_backend_id": map[string]interface{}{
						"type":        "string",
						"description": "Backend ID of the destination daemon (look it up in list_backends)",
					},
					"max_iterations": map[string]interface{}{
						"type":        "integer",
						"description": "Max delta-refresh iterations [0..10], default 3. Each iteration shrinks the dirty set; lower = faster migration but bigger final delta.",
					},
					"delta_threshold_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "If a delta iteration completes in less than this many seconds, skip remaining iterations and cut over. Default 5.",
					},
					"stateful": map[string]interface{}{
						"type":        "boolean",
						"description": "Attempt CRIU-based live migration (preserves process state). Requires CRIU on both ends, doesn't work with podman/docker-in-LXC. Default false.",
					},
				},
				"required": []string{"username", "target_backend_id"},
			},
			Handler: handleMoveContainer,
		},
		{
			Name:        "resize_container",
			Description: "Change a container's CPU / memory / disk allocation in place. At least one of cpu, memory, disk, cpu_request, or memory_request must be provided; the others default to no change. Disk can only grow — the server rejects shrinks. LXC backend: the container stays running, no restart needed for CPU/memory (disk resize is online via ZFS). K8s backend: a running box is stopped and restarted (pod recreate) to apply the new limits; a stopped box picks them up at its next start.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to resize",
					},
					"cpu": map[string]interface{}{
						"type":        "string",
						"description": "New CPU limit (e.g. \"4\" for 4 cores, \"2-4\" for a range). Empty/omitted = no change.",
					},
					"memory": map[string]interface{}{
						"type":        "string",
						"description": "New memory limit (e.g. \"8GB\", \"4096MB\"). Empty/omitted = no change.",
					},
					"disk": map[string]interface{}{
						"type":        "string",
						"description": "New disk size (e.g. \"100GB\"). Can only grow — shrinks are rejected. Empty/omitted = no change.",
					},
					"cpu_request": map[string]interface{}{
						"type":        "string",
						"description": "K8s CPU *request* (K8s backend only, ignored on LXC), separate from cpu which is always applied as the limit. Empty/omitted = the existing request is left alone, even when cpu changes the limit. May be set alone to adjust only the reservation.",
					},
					"memory_request": map[string]interface{}{
						"type":        "string",
						"description": "K8s memory *request* (K8s backend only, ignored on LXC), separate from memory which is always applied as the limit. Same defaulting as cpu_request.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleResizeContainer,
		},
		{
			Name:        "set_secret",
			Description: "Create or update a tenant secret stored encrypted (AES-256-GCM) on the daemon. The value will be stamped as environment.<NAME> on the LXC on next CreateContainer / StartContainer / refresh_secrets. Idempotent — repeated calls bump the version and replace the value. Names must be uppercase env-var-style (^[A-Z_][A-Z0-9_]*$, max 128 chars); values capped at 64 KiB.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Tenant username owning the secret"},
					"name":     map[string]interface{}{"type": "string", "description": "Env-var-style name (uppercase + digits + underscore, e.g. OPENAI_API_KEY)"},
					"value":    map[string]interface{}{"type": "string", "description": "Plaintext value; the daemon encrypts before persisting"},
				},
				"required": []string{"username", "name", "value"},
			},
			Handler: handleSetSecret,
		},
		{
			Name:        "get_secret",
			Description: "Read a single tenant secret's plaintext value. Always audit-logged. Use this to verify what you just wrote or to hand a value to a script via stdout — be mindful where the output goes.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Tenant username"},
					"name":     map[string]interface{}{"type": "string", "description": "Secret name"},
				},
				"required": []string{"username", "name"},
			},
			Handler: handleGetSecret,
		},
		{
			Name:        "list_secrets",
			Description: "List a tenant's secret names + versions + timestamps. Never returns the plaintext values — use get_secret per-name for that (audit-logged separately).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Tenant username"},
				},
				"required": []string{"username"},
			},
			Handler: handleListSecrets,
		},
		{
			Name:        "delete_secret",
			Description: "Remove a tenant secret from the store. Does NOT cascade to env-var stamps on running containers — call refresh_secrets separately if the deletion should reach the next exec without a container restart.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Tenant username"},
					"name":     map[string]interface{}{"type": "string", "description": "Secret name"},
				},
				"required": []string{"username", "name"},
			},
			Handler: handleDeleteSecret,
		},
		{
			Name:        "refresh_secrets",
			Description: "Re-stamp env vars on the LXC from the current secret-store state. Useful after rotation when the next exec'd process should see the new value without a full container restart. Running processes keep their old env (POSIX inherit-at-fork); new execs see the refresh.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Tenant username"},
				},
				"required": []string{"username"},
			},
			Handler: handleRefreshSecrets,
		},
		{
			Name:        "toggle_monitoring",
			Description: "Enable or disable application-emitted OpenTelemetry on an existing container without recreating it. When enabling, the daemon stamps OTEL_EXPORTER_OTLP_ENDPOINT + related env vars and restarts the container so the app picks them up. Use this to retrofit monitoring onto containers created before --monitoring was wired in. Requires the daemon to have an OTel collector endpoint configured.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "true to enable monitoring (stamp OTEL_* env + restart), false to disable (unset OTEL_* env + restart)",
					},
				},
				"required": []string{"username", "enabled"},
			},
			Handler: handleToggleMonitoring,
		},
		{
			Name:        "set_metrics_export",
			Description: "Enable or disable opt-in export of this host's infra metrics to its cloud's native monitoring (GCP Cloud Monitoring in the MVP; disabled by default). Enabling probes the host's credentials synchronously before persisting anything — on GCP this resolves Application Default Credentials with the monitoring-write scope; a host with no usable ADC fails with an actionable error and nothing is exported. Disabling stops emission within one export interval. No daemon restart required either way. Host-level — not available on the hosted control plane, run it against the host's own daemon.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "true to enable export (provider is required), false to disable it (provider is ignored)",
					},
					"provider": map[string]interface{}{
						"type":        "string",
						"description": "Cloud provider to export to. Required when enabled=true. Only \"gcp\" is implemented; \"aws\" is a reserved value the daemon rejects with an unimplemented error.",
						"enum":        []string{"gcp", "aws"},
					},
					"groups": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string", "enum": []string{"host", "container", "platform"}},
						"description": "Metric groups to enable (#1081). Omit to keep host-only (the default). Each group is an independently billed series set; \"container\" and \"platform\" are reserved (their series land in follow-up issues) so enabling them today exports no extra series. Ignored when enabled=false.",
					},
				},
				"required": []string{"enabled"},
			},
			Handler: handleSetMetricsExport,
		},
		{
			Name:        "get_metrics_export",
			Description: "Get the current cloud-native metrics export configuration and last-known health for this host (enabled, provider, interval, last success time, last error, failure count). Host-level — not available on the hosted control plane, run it against the host's own daemon.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleGetMetricsExport,
		},
		{
			Name:        "delete_container",
			Description: "Delete a container permanently",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to delete",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"description": "Force delete even if container is running (default: false)",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleDeleteContainer,
		},
		{
			Name:        "start_container",
			Description: "Start a stopped container. When waitForReady is true the daemon blocks until the container's primary TCP port (from its route record) accepts, or the 30s probe timeout elapses — the response then reports whether the probe timed out.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to start",
					},
					"waitForReady": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, block until the container's primary TCP port accepts or 30s elapses (default false)",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleStartContainer,
		},
		{
			Name:        "toggle_auto_sleep",
			Description: "Opt a container into auto-sleep. Enabled containers are stopped after `idleThresholdMinutes` of network inactivity, which frees their RAM and CPU. They are restarted on the next HTTP request (Phase 3, coming). This sets a per-container metadata flag; it doesn't sleep the container itself — use `stop_container` if you want to sleep it now.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "true to opt in, false to opt out",
					},
					"idleThresholdMinutes": map[string]interface{}{
						"type":        "integer",
						"description": "Idle minutes before Phase 2 would sleep the container. Default 15. Ignored when enabled=false.",
					},
				},
				"required": []string{"username", "enabled"},
			},
			Handler: handleToggleAutoSleep,
		},
		{
			Name:        "stop_container",
			Description: "Stop a running container",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of the container to stop",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"description": "Force stop (kill) instead of graceful shutdown (default: false)",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleStopContainer,
		},
		{
			Name:        "get_metrics",
			Description: "Get runtime metrics (CPU, memory, disk, network) for containers",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Username of specific container (optional, empty for all containers)",
					},
				},
			},
			Handler: handleGetMetrics,
		},
		{
			Name:        "get_system_info",
			Description: "Get information about the Containarium host system",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleGetSystemInfo,
		},
		{
			Name:        "check_for_updates",
			Description: "Check whether a newer Containarium release is available. Returns the running daemon version, the latest GitHub release (cached), and whether an update is available.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleCheckForUpdates,
		},
		{
			Name:        "upgrade_backend",
			Description: "Upgrade a backend's daemon to the binary the sentinel currently serves, immediately instead of waiting for the periodic auto-update tick. The daemon SHA-verifies and smoke-tests the binary, swaps it atomically (keeping the old one as .old), and restarts. Admin-only. Omit backend_id (or pass the local id) to upgrade this daemon; pass a peer id from list_backends to upgrade that peer. Async: a successful local upgrade restarts the daemon, so confirm the new version via list_backends.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"backend_id": map[string]interface{}{
						"type":        "string",
						"description": "Backend to upgrade (from list_backends). Empty = the local/primary daemon.",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"description": "Upgrade even if the sentinel-served binary already matches the running one. Default false.",
					},
					"github_tag": map[string]interface{}{
						"type":        "string",
						"description": "Install this release (e.g. v0.82.0) directly from GitHub Releases instead of the sentinel-served binary. Empty = sentinel path.",
					},
					"allow_downgrade": map[string]interface{}{
						"type":        "boolean",
						"description": "Permit github_tag to be older than the running version (a deliberate rollback). Default false: an older tag is refused.",
					},
				},
			},
			Handler: handleUpgradeBackend,
		},
		{
			Name:        "get_upgrade_status",
			Description: "Poll an upgrade started by upgrade_backend. Returns the status (in_progress, completed, failed, noop, unknown), the version at trigger time, and any error. Note: a successful LOCAL upgrade restarts the daemon and drops the job, so this returns 'unknown' afterward — compare the backend version in list_backends to confirm.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"upgrade_id": map[string]interface{}{
						"type":        "string",
						"description": "Upgrade id returned by upgrade_backend.",
					},
				},
				"required": []string{"upgrade_id"},
			},
			Handler: handleGetUpgradeStatus,
		},
		{
			Name: "list_backends",
			Description: "List the cluster's backend hosts (the local daemon plus any " +
				"tunnel-connected peers). Returns id, type (local/tunnel), health, " +
				"hostname, OS, container count, GPU inventory, and live host load " +
				"(hostLoad: 1/5/15-minute CPU load averages with the host's core " +
				"count, plus memory and disk used vs total) per backend. Use " +
				"this when the agent needs to reason about peer topology or current " +
				"machine loading — e.g. \"which host has GPU capacity?\", \"is peer X " +
				"healthy?\", or \"how loaded is this machine right now?\". A backend " +
				"with no hostLoad block means the load is UNKNOWN (probe failed or " +
				"peer unreachable), not that the host is idle — never read a missing " +
				"block as spare capacity.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleListBackends,
		},
		{
			Name: "get_backend",
			Description: "Get a single backend's details by ID — same fields as list_backends " +
				"but for one host, plus an explicit \"not found\" error when the ID doesn't " +
				"exist. Useful when an agent has a backend ID from list_backends or from a " +
				"container's backendId field and wants to drill down (\"is this peer healthy?\", " +
				"\"how many containers does it have?\", \"does it have a GPU?\").",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Backend ID, as returned by list_backends or as a container's backendId field.",
					},
				},
				"required": []string{"id"},
			},
			Handler: handleGetBackend,
		},
		{
			Name: "backend_validate_gpu",
			Description: "Check that GPU passthrough works inside an LXC on a backend. The " +
				"daemon launches a throwaway nvidia.runtime LXC, runs nvidia-smi inside, " +
				"tears it down, and returns status + GPU model + driver version. Use before " +
				"provisioning a GPU container, or after a VFIO/driver change. Omit backend_id " +
				"for the local/primary host; pass a peer id (from list_backends) to validate " +
				"that peer's GPU. Admin-only; creates and deletes a short-lived container (~30s).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"backend_id": map[string]interface{}{
						"type":        "string",
						"description": "Backend to validate (from list_backends). Empty = local/primary host.",
					},
					"pci": map[string]interface{}{
						"type":        "string",
						"description": "Validate a specific GPU by PCI address (e.g. '0000:01:00.0'). Empty = all GPUs.",
					},
				},
			},
			Handler: handleBackendValidateGPU,
		},
		{
			Name: "push",
			Description: "Push committed git history into a container via real `git push` over " +
				"SSH (laptop -> sentinel -> sshpiper -> container). On first call, sets up a " +
				"bare git repo at ~/work.git inside the container plus a post-receive hook " +
				"that checks out the working tree to ~/work and optionally runs the configured " +
				"deploy_cmd. On subsequent calls, just runs `git push` — the hook fires " +
				"server-side.\n\n" +
				"This is the *commit-only* + *release* mode. Working-tree changes that aren't " +
				"committed DO NOT ship by default — if the working tree has uncommitted or " +
				"untracked files, the tool refuses with a clear error. Pass `include_wip=true` " +
				"to auto-create a WIP commit, push, and rewind the local repo afterwards.\n\n" +
				"If you want to mirror the entire local state (uncommitted changes + untracked " +
				"files + stash refs) without commit ceremony, use the `sync` tool instead.\n\n" +
				"deploy_cmd: when set, the post-receive hook runs the given shell command " +
				"inside the container's work-tree directory after each successful push. Use " +
				"it for `systemctl restart`, `make build && systemctl restart`, etc. The hook " +
				"is rewritten on every push, so changing deploy_cmd between calls just " +
				"updates the hook.\n\n" +
				"After the first push, a local git remote (default 'containarium-<username>') " +
				"is configured on the local repo. From any clone with that remote, " +
				"`git push containarium-<username> <branch>` works directly without invoking " +
				"this tool — same plumbing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Container username (same value used by create_container).",
					},
					"local_path": map[string]interface{}{
						"type":        "string",
						"description": "Local git repo path (default: current working directory).",
					},
					"branch": map[string]interface{}{
						"type":        "string",
						"description": "Branch to push (default: current HEAD branch).",
					},
					"remote_path": map[string]interface{}{
						"type":        "string",
						"description": "Working-tree directory inside the container (default: ~/work). The bare repo lives at <remote_path>.git.",
					},
					"include_wip": map[string]interface{}{
						"type":        "boolean",
						"description": "If true and the working tree is dirty, auto-create a WIP commit, push, then rewind the local repo. Default false (refuse on dirty tree).",
					},
					"deploy_cmd": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to run on the container after each successful push (inside the work-tree directory). Empty = no deploy hook command, but the working tree is still checked out.",
					},
					"remote_name": map[string]interface{}{
						"type":        "string",
						"description": "Local git remote name to configure. Default 'containarium-<username>'.",
					},
					"sentinel": map[string]interface{}{
						"type":        "string",
						"description": "Sentinel SSH host override. Default: the container's ssh_host (the sentinel it belongs to, reported by the daemon).",
					},
					"key_path": map[string]interface{}{
						"type":        "string",
						"description": "SSH private key path override. Default ~/.containarium/keys/<username>.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handlePush,
		},
		{
			Name: "sync",
			Description: "Mirror a local directory into a container — rsync-style, but using " +
				"a one-shot content-hash diff + tar so it works through Containarium's " +
				"shell stack without needing rsync's bidirectional protocol.\n\n" +
				"Carries everything in the working tree: committed history, uncommitted " +
				"modifications, untracked files, stash refs (because `.git/` is part of " +
				"the working directory). Subsequent calls ship only the content-hash delta.\n\n" +
				"This is the *mirror* mode. Use it when you want the container to exactly " +
				"reflect your local state including WIP. If you want commit-only semantics " +
				"(atomic per commit, refuses on dirty tree), use `push` instead.\n\n" +
				"By default, files that exist on the remote but not locally are LEFT in " +
				"place. Pass `delete=true` for true rsync --delete semantics.\n\n" +
				"Default excludes (substring match): node_modules/, .terraform/, " +
				"__pycache__/, .pytest_cache/, .venv/, venv/, .DS_Store, .idea/, .vscode/. " +
				"Pass an `exclude` array to add to (not replace) the defaults.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Container username (same value used by create_container).",
					},
					"local_path": map[string]interface{}{
						"type":        "string",
						"description": "Local directory to mirror (default: current working directory).",
					},
					"remote_path": map[string]interface{}{
						"type":        "string",
						"description": "Destination directory inside the container (default: ~/work).",
					},
					"delete": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, remove files on the remote that don't exist locally (rsync --delete semantics). Default false.",
					},
					"exclude": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "Additional exclude patterns (substring match), added to the sensible defaults.",
					},
					"sentinel": map[string]interface{}{
						"type":        "string",
						"description": "Sentinel SSH host override. Default: the container's ssh_host (the sentinel it belongs to, reported by the daemon).",
					},
					"key_path": map[string]interface{}{
						"type":        "string",
						"description": "SSH private key path override. Default ~/.containarium/keys/<username>.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleSync,
		},
		{
			Name: "security_scan",
			Description: "Run a security scan on a container. Triggers one or more of the daemon's " +
				"three scanner subsystems: ClamAV (malware in container files), pentest " +
				"(CVE-style vulnerabilities in installed packages), ZAP (web-app DAST against " +
				"any exposed HTTP services).\n\n" +
				"All three scanners honor the `username` argument and scope the scan to that " +
				"single container — pentest filters its target list to routes + container " +
				"records belonging to the user, ZAP filters routes by user, ClamAV scans only " +
				"that container's files. The scan-run records persist the container scope so " +
				"future per-container queries surface only the relevant runs.\n\n" +
				"This is a one-shot operator-invoked action — call it when you suspect or " +
				"need to confirm a container's security posture. The scans run asynchronously " +
				"on the daemon; this tool returns once the trigger is accepted. After waiting " +
				"(see `pollHint` in the response), call `security_findings` to read results.\n\n" +
				"Typical durations:\n" +
				"  - clamav: minutes; runs inside the box (needs 4 GB RAM and ClamAV installed there), started by the box owner\n" +
				"  - pentest: tens of seconds (per-target probe)\n" +
				"  - zap: minutes (active spider + attack passes)\n\n" +
				"For continuous/scheduled scanning, the cloud product offers a hosted " +
				"security-patch agent (see the cloud roadmap). This OSS tool is the one-shot " +
				"BYOA equivalent.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Container username (same value used by create_container).",
					},
					"kind": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"clamav", "pentest", "zap", "all"},
						"description": "Which scanner(s) to run. Default 'all' triggers all three.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleSecurityScan,
		},
		{
			Name: "security_findings",
			Description: "List security findings for a container, normalized across the three " +
				"scanner subsystems (ClamAV, pentest, ZAP). Returns a single shape per finding " +
				"so the agent doesn't have to branch on scanner internals:\n" +
				"  - kind:          'clamav' | 'pentest' | 'zap'\n" +
				"  - id:            daemon-side row ID; pass to security_remediate\n" +
				"  - severity:      'critical' | 'high' | 'medium' | 'low' | 'info'\n" +
				"  - title:         short description\n" +
				"  - description:   detail (optional)\n" +
				"  - containerName: which container the finding pertains to\n" +
				"  - target:        URL/IP:port for pentest+ZAP (optional)\n" +
				"  - fixAvailable:  true → `security_remediate` can act on this row.\n\n" +
				"Today only pentest findings have `fixAvailable=true` (the daemon's " +
				"`RemediatePentestFinding` runs a package upgrade). ClamAV findings need " +
				"a quarantine workflow; ZAP findings need web-app code fixes — neither has " +
				"an auto-fix path yet. Filter to one scanner with `kind`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Container username.",
					},
					"kind": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"clamav", "pentest", "zap", "all"},
						"description": "Restrict to one scanner. Default 'all' merges results.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleSecurityFindings,
		},
		{
			Name: "security_remediate",
			Description: "Attempt to auto-fix a security finding. Currently only works on " +
				"pentest-kind findings where `fixAvailable=true` (the daemon's " +
				"`RemediatePentestFinding` runs a package upgrade). ClamAV and ZAP findings " +
				"return an error here — they need different fix workflows that don't exist " +
				"yet.\n\n" +
				"This is a one-shot, operator-confirmed action. Do NOT chain " +
				"`security_scan → security_findings → security_remediate` autonomously " +
				"without user confirmation — surface the findings to the user first, let " +
				"them decide which to fix.\n\n" +
				"Returns `success`, `packageName`, `oldVersion`, `newVersion` on a successful " +
				"package upgrade. A failed remediate doesn't put the finding into a wedged " +
				"state — the same finding can be remediated again after fixing the underlying " +
				"reason (locked package, missing apt index, etc.).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"finding_id": map[string]interface{}{
						"type":        "integer",
						"description": "Finding ID from security_findings (`id` field).",
					},
				},
				"required": []string{"finding_id"},
			},
			Handler: handleSecurityRemediate,
		},
		{
			Name: "security_sentry_status",
			Description: "Report the background threat-detection engine's on/off state (#1640): " +
				"'OK' (running, findings persisted), 'DEGRADED' (running, but no Postgres — " +
				"findings don't survive a daemon restart; or the process responder cannot apply " +
				"THROTTLE/FREEZE (responses disabled); see `reason`), 'UNAVAILABLE' (no eBPF object loaded, " +
				"or no audit store — nothing is being detected; check `reason`), or 'DISABLED' " +
				"(CONTAINARIUM_THREAT_SENTRY unset). Also lists every registered detection rule's " +
				"health — a rule that panicked shows healthy=false with lastError set, but the " +
				"engine and every other rule keep running.\n\n" +
				"Call this before trusting the absence of security findings: UNAVAILABLE/DISABLED " +
				"mean nothing is watching, not that nothing is wrong. Takes no arguments.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleSecuritySentryStatus,
		},
		{
			Name: "list_bad_destinations",
			Description: "List the known-bad-destination list (#1641) the threat-detection sentry's " +
				"bad-destination rule matches flow destinations against — a merged view of the " +
				"embedded baseline (mining pools, versioned) and any operator-added entries. " +
				"Takes no arguments.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleListBadDestinations,
		},
		{
			Name: "add_bad_destination",
			Description: "Add an operator-supplied entry (exact IP or CIDR) to the known-bad-destination " +
				"list, effective immediately — no daemon rebuild or restart required. Use this to " +
				"extend detection to a destination the embedded baseline list doesn't cover yet.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"cidr": map[string]interface{}{
						"type":        "string",
						"description": "Exact IP (\"203.0.113.7\") or CIDR (\"203.0.113.0/24\").",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Human-readable reason, e.g. \"reported mining pool\".",
					},
				},
				"required": []string{"cidr"},
			},
			Handler: handleAddBadDestination,
		},
		{
			Name: "remove_bad_destination",
			Description: "Remove a previously operator-added entry from the known-bad-destination list. " +
				"Baseline (embedded, versioned) entries cannot be removed this way.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"cidr": map[string]interface{}{
						"type":        "string",
						"description": "Exact IP or CIDR to remove, as it was added.",
					},
				},
				"required": []string{"cidr"},
			},
			Handler: handleRemoveBadDestination,
		},
		{
			Name: "list_security_sentry_findings",
			Description: "List security findings raised by the background threat-detection sentry " +
				"(#1639-#1642) — mining-abuse egress, cross-tenant flow, and deny-burst detections — " +
				"with evidence (triggering flow 5-tuples / deny counts). Distinct from " +
				"`security_findings`, which is the unrelated ClamAV/pentest/ZAP scanner surface.\n\n" +
				"Every filter is optional. Non-admin callers only ever see their own tenant, " +
				"regardless of what (if anything) `tenant` is set to.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"severity": map[string]interface{}{
						"type":        "string",
						"description": "Filter by severity: low, medium, high, critical. Omit for any severity.",
					},
					"tenant": map[string]interface{}{
						"type":        "string",
						"description": "Filter by tenant id (admin only — a non-admin caller's own tenant is used regardless).",
					},
					"since": map[string]interface{}{
						"type":        "string",
						"description": "RFC3339 timestamp; only findings last seen at or after this time.",
					},
					"state": map[string]interface{}{
						"type":        "string",
						"description": "Filter by state: open, resolved. Omit for any state.",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max findings to return. 0 (default) means the server default (50, capped at 200).",
					},
				},
			},
			Handler: handleListSecuritySentryFindings,
		},
		{
			Name:        "resolve_security_sentry_finding",
			Description: "Mark an open threat-detection sentry finding as resolved, after triaging and handling it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "Finding id, from list_security_sentry_findings.",
					},
				},
				"required": []string{"id"},
			},
			Handler: handleResolveSecuritySentryFinding,
		},
		{
			Name: "install_zap",
			Description: "Download and install OWASP ZAP into this host's security container. " +
				"Admin-only, one-time-per-host setup — `security_scan` (kind=zap) and the " +
				"scheduled ZAP scan cycle both require this to have been run at least once.\n\n" +
				"Without it, every ZAP scan job on an uninstalled host used to fail after a " +
				"generic 120-second timeout, indistinguishable from a slow daemon start, and " +
				"repeat forever on every subsequent scan (#960) — this call is what closes " +
				"that gap. Safe to call again on an already-installed host; it reports " +
				"'already installed' instead of erroring.\n\n" +
				"Takes no arguments — a daemon manages exactly one security container, so " +
				"there's no per-container or per-host identifier to pass.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: handleInstallZap,
		},
		{
			Name: "install_clamav",
			Description: "Install ClamAV and its signatures inside ONE box so the tenant-run malware scan " +
				"(`security_scan` with kind=clamav) can run. Started by the box owner; the platform " +
				"runs the package install in the box and does not read the box's files.\n\n" +
				"Requires a running Debian/Ubuntu box with at least 4 GB RAM and outbound access to the " +
				"package mirror and the ClamAV signature servers. Idempotent: an already-installed box " +
				"runs nothing and reports ALREADY_PRESENT. Other OS families report UNSUPPORTED_FAMILY. " +
				"Can take several minutes.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"container_name": map[string]interface{}{
						"type":        "string",
						"description": "The box to install into, e.g. alice-container.",
					},
				},
				"required": []string{"container_name"},
			},
			Handler: handleInstallClamav,
		},
		{
			Name: "sync_ssh_config",
			Description: "Generate a self-contained ssh_config covering every reachable container " +
				"and write it to ~/.containarium/ssh_config. After this call, `ssh <username>` " +
				"works from any shell — no CLI install required.\n\n" +
				"Call this right after create_container so the new container's SSH alias is " +
				"available immediately. The first time you use it, you also need to add a single " +
				"line to your ~/.ssh/config to wire it in:\n" +
				"  Include ~/.containarium/ssh_config\n" +
				"That's a one-time setup; subsequent sync calls just refresh the file.\n\n" +
				"Two modes:\n" +
				"  - Direct (default): each container's IP is the SSH HostName. Works when the " +
				"    container's LAN IP is reachable from where you're running ssh.\n" +
				"  - Via sentinel (set `sentinel`): all containers route through the sentinel " +
				"    via sshpiper. Use this when containers don't have public IPs.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"sentinel": map[string]interface{}{
						"type":        "string",
						"description": "Sentinel SSH endpoint (e.g. sentinel.example.com or sentinel.example.com:2222). Empty = direct mode.",
					},
					"identity_file": map[string]interface{}{
						"type":        "string",
						"description": "IdentityFile path to render in every Host block. For ephemeral-keypair containers, agents typically pass ~/.containarium/keys/<username>.",
					},
					"include_stopped": map[string]interface{}{
						"type":        "boolean",
						"description": "Include stopped containers in the config. Default false (only running containers).",
					},
					"out": map[string]interface{}{
						"type":        "string",
						"description": "Output path override. Default: ~/.containarium/ssh_config.",
					},
					"force": map[string]interface{}{
						"type": "boolean",
						"description": "Write even when this run generated 0 hosts. Off by default: a zero-host " +
							"result usually means an expired credential or a control plane that cannot see " +
							"your boxes, and overwriting a working config with it loses aliases you cannot " +
							"regenerate. The previous file is kept as <out>.bak either way.",
					},
				},
			},
			Handler: handleSyncSSHConfig,
		},
		{
			Name: "code_run",
			Description: "Start a coding agent on a box and return the first window of its output. " +
				"The run is DETACHED: it survives this call returning, the MCP connection dropping, and " +
				"the client going away — that is the point. The response carries `next_offset`; pass it " +
				"to code_attach to continue reading exactly where this left off, with nothing skipped " +
				"and nothing repeated. Requires the box to already have the toolchain installed " +
				"(code_install).",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box", "prompt"},
				"properties": map[string]interface{}{
					"box":    map[string]interface{}{"type": "string", "description": "Box (container username) to run on."},
					"prompt": map[string]interface{}{"type": "string", "description": "The task to give the agent."},
					"name": map[string]interface{}{"type": "string",
						"description": "Run name, default \"code\". Override to run more than one task concurrently on the same box; the same name is how code_attach/code_status/code_stop find it later."},
					"stream_json": map[string]interface{}{"type": "boolean",
						"description": "Capture stdout and stderr separately (framed) so a JSON stream on stdout is not corrupted by diagnostics. Default false."},
					"session_id": map[string]interface{}{"type": "string",
						"description": "Resume this specific session id (claude --resume) instead of starting a fresh conversation. From a previous code_runs/BoxRun.session_id, when the engine exposed one."},
				},
			},
			Handler: handleCodeRun,
		},
		{
			Name: "code_attach",
			Description: "Resume a run's output from a byte offset. Pass the `next_offset` from your " +
				"previous code_run/code_attach call — carrying the offset forward is what makes " +
				"reconnection lossless. Omit it (or 0) to replay the run from the beginning. Reports " +
				"whether the run is still going, so you know when to stop polling.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box"},
				"properties": map[string]interface{}{
					"box":  map[string]interface{}{"type": "string", "description": "Box the run is on."},
					"name": map[string]interface{}{"type": "string", "description": "Run name, default \"code\"."},
					"offset": map[string]interface{}{"type": "integer",
						"description": "Byte offset to resume from — the `next_offset` of your last call. 0 replays from the start."},
					"stream_json": map[string]interface{}{"type": "boolean",
						"description": "Set if the run was started with stream_json, so framed output is demultiplexed."},
				},
			},
			Handler: handleCodeAttach,
		},
		{
			Name:        "code_status",
			Description: "Report a run's liveness and, once it has finished, its exit code — including for a run that finished while nothing was attached.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box"},
				"properties": map[string]interface{}{
					"box":  map[string]interface{}{"type": "string", "description": "Box the run is on."},
					"name": map[string]interface{}{"type": "string", "description": "Run name, default \"code\"."},
				},
			},
			Handler: handleCodeStatus,
		},
		{
			Name:        "code_stop",
			Description: "Stop a run. Its log is left readable, so output can still be collected afterwards with code_attach.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box"},
				"properties": map[string]interface{}{
					"box":   map[string]interface{}{"type": "string", "description": "Box the run is on."},
					"name":  map[string]interface{}{"type": "string", "description": "Run name, default \"code\"."},
					"force": map[string]interface{}{"type": "boolean", "description": "SIGKILL instead of SIGTERM. Default false."},
				},
			},
			Handler: handleCodeStop,
		},
		{
			Name: "code_runs",
			Description: "List the code runs on a box (containarium code run) through the daemon API, " +
				"no SSH needed: name, outcome (running, exited, or unknown when the run was killed " +
				"or the box restarted), exit code, start/end time, log path. Same function as " +
				"`containarium code runs`.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box"},
				"properties": map[string]interface{}{
					"box": map[string]interface{}{"type": "string", "description": "Box (owner username) whose runs to list."},
				},
			},
			Handler: handleCodeRuns,
		},
		{
			Name: "code_logs",
			Description: "Read one window of a code run's output through the daemon API, no SSH " +
				"needed. Returns the bytes plus end_offset; pass end_offset back as start_offset " +
				"to continue, like tail_log. ended=true once the run has exited and its log is " +
				"read to the end. A framed (stream-json) run's stderr is left out and flagged by " +
				"stderr_dropped. Same reader as `containarium code logs`.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box", "run"},
				"properties": map[string]interface{}{
					"box":            map[string]interface{}{"type": "string", "description": "Box (owner username) the run is on."},
					"run":            map[string]interface{}{"type": "string", "description": "Run name, from code_runs."},
					"start_offset":   map[string]interface{}{"type": "number", "description": "Byte offset to read from (default 0)."},
					"follow_seconds": map[string]interface{}{"type": "number", "description": "Wait up to this many seconds (0-10) for new bytes when none are available."},
				},
			},
			Handler: handleCodeLogs,
		},
		{
			Name: "code_pending",
			Description: "List the open permission requests of a code run started with " +
				"--approvals ask: request id, age, time left before it expires into a deny, " +
				"and the prompt (tool and command or path), plus the run's request counts. " +
				"Same table as `containarium code pending`, then `---` and the same data as " +
				"JSON, whose `tool` and `input` fields (each request's tool and complete input) are what code_answer takes. The list is read from " +
				"a journal the agent on the box can write, so it is advisory.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"box"},
				"properties": map[string]interface{}{
