# Containarium — Agent Runtime

> **Open-source agent runtime** · SSH-native isolation · eBPF egress policy · Kubernetes + LXC · MCP-native CLI · GPU passthrough

**The open-source, self-hostable agent runtime for AI agents.**
Each agent gets a persistent, SSH-reachable box in an unprivileged container, with an opt-in per-tenant eBPF egress policy, audit-logged denials, and a model gateway that keeps provider API keys out of the box. What is on by default, and what each claim rests on, is in [`docs/security/CLAIMS.md`](docs/security/CLAIMS.md).

Bring your own agent — Cursor, Claude Code, OpenCode, your own MCP client.
We run the box.

```
agent: "create me a sandbox called 'blog'"           → containarium create
agent: "wire up SSH so I can reach it"               → containarium ssh-config sync
agent: "install Caddy on :8080 inside the box"       → shell_exec (via agent-box MCP)
agent: "expose that on blog.example.com"             → containarium expose-port

curl https://blog.example.com → hello world
```





🌐 **Project site:** [containarium.dev](https://containarium.dev) · 🎬 **55s demo:** [youtu.be/IBDDD_tb8FY](https://youtu.be/IBDDD_tb8FY) · 🚀 **Live app:** [helloworld.demo.containarium.dev](https://helloworld.demo.containarium.dev)

---

## Choose your path

| Path | You end up with | Go to |
|------|-----------------|-------|
| **Hosted cloud** | a box you can `ssh` into with your agent wired to it, no server to run | [Sign up at cloud.containarium.dev](https://cloud.containarium.dev) |
| **Self-host on a VM** | the same, on your own Ubuntu VM (about 5 minutes) | [Quick start](#quick-start) |
| **See the end result** | a live app served from a box, which is what you end up with | [helloworld.demo.containarium.dev](https://helloworld.demo.containarium.dev) |

---

## Why an agent runtime?

AI agents are increasingly the primary user of dev infrastructure. They
want to build, install, deploy, and verify — not on the human's laptop
(too noisy, too risky, too local) but on a persistent runtime that's:

- **Persistent**: state survives between agent runs.
- **Separate from your machine**: a misbehaving install doesn't touch your laptop or your cluster. (Tenant-to-tenant isolation is not on by default; see [`docs/security/CLAIMS.md`](docs/security/CLAIMS.md).)
- **Real**: a full Linux environment with `systemd`, real networking,
  and the ability to host things on the open internet.
- **Driven by structured tools**: not by an agent typing commands into a
  TTY hoping nothing scrolls off-screen, but by MCP — typed,
  bounded, safe.
- **Blast-radius-bounded**: the agent holds an SSH key, not a
  kube-apiserver token. It can't reach the cluster control plane, the
  host OS, or other tenants' boxes.

That's the runtime Containarium gives you. It runs as a self-hosted
platform on LXC or Kubernetes, exposes its admin surface over MCP, and
ships a second MCP server that lives *inside* the box so the agent can
`shell_exec` and edit files directly.

You bring the agent. We run the box.

---

## Quick start

One path, three steps, with a check at the end of each. You're done when a
box exists, `ssh alice` gets you a shell in it, and your agent can run
commands inside it.

**Before you start:** a fresh Ubuntu 24.04 VM you can `sudo` on (see
[System requirements](#system-requirements)). You need Containarium
**v0.96.0 or later**; the installer fetches the latest release. `sudo` is
only needed to run the installer and to read the admin token. Every other
command runs as your normal user and goes through the daemon's API.

### 1. Install, then create your first box

On the VM:

```bash
curl -fsSL https://containarium.dev/install.sh | sudo bash
```

✅ **Check:** the daemon is running and the CLI is on your `PATH`.

```bash
systemctl is-active containarium   # → active
containarium version               # → v0.96.0 or later
```

Point the CLI at the local daemon, using the admin token the installer
saved, and create a box called `alice`:

<!-- The published installer (https://containarium.dev/install.sh) now carries
     `--quickstart <name>` (checked 2026-10-15; #2244 is closed). Flip this
     block to the one-liner (`… | sudo bash -s -- --quickstart alice`) once a
     fresh-VM run of it has been recorded — not before. -->

```bash
export CONTAINARIUM_HTTP=true
export CONTAINARIUM_SERVER=http://localhost:8080
export CONTAINARIUM_TOKEN="$(sudo cat /etc/containarium/admin.token)"

containarium quickstart alice
```

`quickstart` creates the box, reuses your `~/.ssh` key or generates a
managed one (`~/.ssh/containarium_ed25519`), writes
`~/.containarium/ssh_config`, adds the single
`Include ~/.containarium/ssh_config` line to `~/.ssh/config`, and wires
the box into your agent's MCP config (Claude Code by default). It's safe
to run again, including to repair an older quickstart's MCP command.

✅ **Check:** `quickstart` ends with `✓ quickstart complete`, and the box is
listed as running.

```bash
containarium list   # → a row for alice-container, STATUS running
```

### 2. Connect to the box

Choose the variant that matches where you're typing.

**Variant A: on the VM.** This works on a fresh install. Step 1 already
wrote the config. Re-sync whenever your boxes change:

```bash
containarium ssh-config sync --identity ~/.ssh/containarium_ed25519
# leave out --identity if quickstart reused your own ~/.ssh key
```

✅ **Check:**

```bash
ssh alice hostname   # → alice-container
```

**Variant B: from your laptop.** On a single VM, `ssh-config sync` adds a
`ProxyJump` through the VM's per-user jump account when the daemon doesn't
advertise `ssh_host`. If the API is reached directly, the jump host defaults
to the API hostname, on SSH port 22. With the API tunnel below, name the
actual VM with `--jump-host` (use `<vm-host>:<ssh-port>` for a custom SSH port).
Deployments with a [sentinel](#sentinel--sshpiper--caddy--proxy-protocol) can
use `--sentinel <sentinel-host>` instead; a daemon's `--ssh-host` still takes
precedence over the jump route.

```bash
# On your laptop: install the client only.
curl -fsSL https://raw.githubusercontent.com/hamzaaxnvan/containarium/main/hacks/install-cli.sh | sudo bash

# Reach the daemon's API through an SSH tunnel to the VM.
ssh -fN -L 8080:localhost:8080 <you>@<vm-host>
export CONTAINARIUM_HTTP=true
export CONTAINARIUM_SERVER=http://localhost:8080
export CONTAINARIUM_TOKEN="$(ssh <you>@<vm-host> sudo cat /etc/containarium/admin.token)"

containarium ssh-config sync --jump-host <vm-host>
```

Then make `Include ~/.containarium/ssh_config` the first line of your
laptop's `~/.ssh/config`. Use the key whose public half was supplied when
creating the box: that key is authorized on both the box and its VM jump
account. Add `--identity ~/.ssh/<your-private-key>` to sync if needed; it
applies to both SSH connections.

✅ **Check:**

```bash
ssh alice-container hostname   # → alice-container
```

### 3. Wire your agent to the box

Your agent reaches the box through `agent-box`, an MCP server that runs
inside it. `code install` puts it there, along with Claude Code. Run it on
the VM, in the same shell as step 1:

```bash
containarium code install alice
```

✅ **Check:** the install prints `agent-box installed at …/.local/bin/agent-box`,
and the binary is there:

```bash
ssh alice 'test -x ~/.local/bin/agent-box && echo agent-box ready'   # → agent-box ready
```

On the machine where your agent runs, `quickstart` wires the MCP config
automatically. Step 1 already did this for Claude Code on the VM. If your
agent runs on your laptop, run this there using the API connection from
step 2; it reuses the existing box:

```bash
containarium quickstart alice --agent claude
# For Gemini or Codex, use --agent gemini or --agent codex instead.
```

For Cursor, add the server to `~/.cursor/mcp.json` manually:

```jsonc
{
  "mcpServers": {
    "containarium-box": {
      "command": "ssh",
      "args": ["alice", "sh -c 'PATH=\"$HOME/.local/bin:/usr/local/bin:$PATH\" exec agent-box'"]
    }
  }
}
```

This command sets the box's `PATH` for non-interactive SSH and supports
both user-level and system-wide installs of `agent-box`.

✅ **Check:** your agent lists `containarium-box` as a connected MCP server
(in Claude Code, `claude mcp list`). Asking it to *"run `uname -a` in the
box"* returns a Linux kernel line from `alice-container`. It now has
`shell_exec`, `read_file`, `write_file`, `list_directory`, `move_file` and
`delete_file` inside the box.

**That's your first box.** Next:
[run the agent on the box](#run-the-agent-on-the-box) ·
[run pi inside the box](#run-pi-inside-the-box) ·
[put the box on a public hostname](#put-the-box-on-a-public-hostname) ·
[build a site in one command](#build-a-site-in-one-command).

---

## After your first box

### Run the agent on the box

`code install` (step 3) already put Claude Code on the box. It installs
**no credential**: sign-in goes through Anthropic's own flow, never through
us. Choose one of these, once:

```bash
# interactive: sign in inside the box (device code)
ssh alice
claude

# or headless: your own key in the "env" block of ~/.claude/settings.json on the box
#   {"env": {"ANTHROPIC_API_KEY": "<your key>"}}
```

Then:

```bash
containarium code run alice --prompt "add a health endpoint and run the tests"
```

`code run` streams output to your terminal as it's produced, but it isn't a
pipe. The run is detached on the box and its output is captured to a log;
your terminal is a *resumable reader* over that log. Close your laptop, lose
wifi, press Ctrl-C: the run keeps going, and `containarium code attach alice`
picks the stream back up **byte-exact**, with nothing missing and nothing
repeated.

```bash
containarium code attach alice   # reconnect; replays what you missed
containarium code status alice   # liveness, and the exit code once it finishes
containarium code stop alice     # reap it; the log stays readable
```

The difference from the MCP wiring in step 3 is where the agent runs. There,
the agent is on your machine and reaches into the box. Here, the agent *is*
on the box, so the work survives your machine sleeping, and the tests run
next to the code instead of over a network hop.

### Run pi inside the box

If your agent has no MCP client, run it *inside* the box instead. See
[docs/integrations/pi.md](docs/integrations/pi.md) for the
[pi](https://pi.dev) walkthrough (installs, keys, code sync, sessions).

### Put the box on a public hostname

```bash
containarium expose-port alice \
  --container-port 8080 \
  --domain blog.example.com
```

Caddy on the sentinel terminates TLS for `blog.example.com` and forwards to
`alice-container:8080`. `curl https://blog.example.com` then hits whatever
`alice` is serving on port 8080.

### Build a site in one command

Run from your laptop against a server you can reach (see step 2, variant B),
`quickstart` does steps 1–3 in one go: it creates the box and wires SSH and
your agent (`claude`, `gemini` or `codex`). With `--prompt` it also exposes
`--domain` and launches your local agent on the build:

```bash
containarium quickstart alice --server <your-server> \
  --prompt "a coffee-shop landing page" --domain coffee.example.com
```

`containarium quickstart --help` lists every option.

---

## The four primitives

Every action in Containarium has a CLI verb (canonical) AND an MCP tool
(thin wrapper that delegates to the same Go function). See
[CLAUDE.md](CLAUDE.md) for the convention.

### `agent-box` — in-the-box MCP server

Runs inside every container. Reached over stdio (typically wrapped by
SSH on the client side). Exposes Linux-native operations:

| Tool | What it does |
|---|---|
| `shell_exec` | Run a shell command, capture stdout/stderr/exit, bounded by timeout (default 30s, max 10min) and 256 KiB output cap |
| `read_file` | Byte range OR `head=N` lines OR `tail=N` lines |
| `write_file` | Atomic write with `mkdirp` (temp + rename) |
| `list_directory` | Type/size/mtime, hidden filtering |
| `move_file` | Atomic rename with `mkdirp` on destination |
| `delete_file` | Single-file remove (refuses directories so recursive deletes go via `shell_exec` where blast radius is explicit) |

Resources (read-only data the agent fetches via MCP `resources/read`):

| URI | What it returns |
|---|---|
| `containarium://ci-context` | JSON metadata about the current CI run (PR number, commit SHA, failing test, etc.) when the box was kept alive by the FootprintAI/containarium-run GitHub Action after a failed CI run. Returns `{"available": false}` on non-CI boxes so callers never have to special-case errors. |
| `containarium://ci-prompt` | Static markdown playbook telling agents how to debug a failing CI run inside this box (what to read first, how to iterate, what not to do). Same body on every box; pair with `ci-context` for the per-run data. |

Optional sandbox: when `AGENTBOX_ROOT` is set, every file-ops path is
resolved against that root with a boundary-aware prefix check. Default
unset = no constraint. See
[`internal/agentbox/`](internal/agentbox/) for the Go implementation.

### `mcp-server` — platform MCP server

Runs on the host. Exposes the platform's outside-the-box operations as
MCP tools — about a hundred of them, one per CLI verb: container lifecycle
(`create_container`, `list_containers`, `start_container`,
`stop_container`, `delete_container`, `resize_container`), networking
(`expose_port`, `sync_ssh_config`, passthrough routes), coding-agent runs
(`code_run`, `code_attach`, `code_status`, `code_stop`), secrets, backups,
recipes, security scans, tracker connections, and `get_metrics` /
`get_system_info`. A token's scopes decide which ones a client sees. See
[`cmd/mcp-server/`](cmd/mcp-server/) and
[`internal/mcp/`](internal/mcp/) for the catalog.

### `containarium` CLI

Same surface as the platform MCP, plus deeper administration. The verbs
this README uses (the CLI has about ninety — `containarium --help` lists
them all):

```
containarium quickstart         Create a box, wire SSH + your agent, optionally build a site
containarium create             Create a new container
containarium list               List all containers
containarium wake / sleep       Start a stopped box / stop a running one
containarium delete             Delete a container
containarium code               Run a coding agent ON a box (install / run / attach / logs / runs / status / stop / egress-policy)
containarium expose-port        Expose container:port on a public hostname
containarium ssh-config         Generate self-contained ssh_config (show / sync)
containarium route              Manage proxy routes (low-level)
containarium passthrough-route  Manage TCP/UDP passthrough routes (daemon API)
containarium token              Issue, inspect, refresh and revoke JWT tokens
containarium login / connect    Device-flow sign-in to a server; open a shell on a box with that token
containarium info               Container or system info
containarium version            Print version
```

Host-side verbs (`daemon`, `sentinel`, `passthrough`, `audit`, `doctor`,
…) live in `containariumd` — see [Two binaries](#two-binaries-containarium-vs-containariumd).
Run `containarium <verb> --help` for full options.

### Sentinel — sshpiper + Caddy + PROXY-protocol

The sentinel is a tiny always-on VM (e2-micro on GCP free tier works)
that:

- Receives SSH on port 22 (sshpiper routes to the right backend by
  username).
- Receives HTTPS on 443 (Caddy with TLS-passthrough or
  PROXY-protocol-aware forwarding to backend Caddy).
- Survives spot-VM termination on the backend with a maintenance page.
- Holds the static IP / DNS A-record so backends can be ephemeral.

See [docs/SENTINEL-DESIGN.md](docs/SENTINEL-DESIGN.md) for the full
design.

---

## Architecture

```
        Agent (Cursor / Claude Code / OpenCode)
            │
            │ JWT (access; tt=access, jti, scopes)
            │ MCP over stdio  ──┐
            │                   │ ┌── refresh ──> POST /v1/tokens/refresh
            v                   ▼ │                  (single-use; old jti revoked)
        ssh user@box  → sshpiper → agent-box (in container)
            │
            │ HTTPS  (mTLS upstream; PROXY-protocol v2)
            v
        Sentinel (e2-micro, always-on)
        ├── sshpiper (port 22)            : routes by username; fail2ban per-user
        ├── Caddy + PROXY-protocol (443)  : routes by hostname / SNI suffix
        └── /wake/ source-IP allowlist    : trusted-proxy only
            │
            v
        +-------------------------------------------------+
        | Backend VM (spot or bare-metal GPU node)        |
        |                                                 |
        |  Incus (LXC) ── containers                      |
        |    ├── alice-container    : SSH + agent-box     |
        |    │   └── /run/secrets/* : tmpfs, 0440 alice   |
        |    └── bob-container      : ZFS-backed storage  |
        |                                                 |
        |  Containarium daemon                            |
        |    ├── JWT auth (iss/aud/jti/scopes)            |
        |    ├── Admin RBAC + container-owner authz       |
        |    ├── Image-digest gate (REQUIRE + VERIFY)     |
        |    ├── Secrets ── Postgres (envelope-encrypted) |
        |    │              │                             |
        |    │              v                             |
        |    │           KMS ── Vault Transit / GCP KMS   |
        |    │           (master key retirable post-cutover)
        |    └── Audit log ── Postgres + SHA-256 hash     |
        |                     chain (verify CLI)          |
        +-------------------------------------------------+
```

A single sentinel can front multiple backend VMs — a "pool" — and a
single deployment can run multiple pools (each with its own backends). See
[docs/MULTI-POOL.md](docs/MULTI-POOL.md).

**Security control surface** (all opt-in via env, default-off for
upgrade safety; see [`docs/security/OPERATOR-SECURITY-RUNBOOK.md`](docs/security/OPERATOR-SECURITY-RUNBOOK.md)):

| Env var | Layer | Effect |
| --- | --- | --- |
| `CONTAINARIUM_REQUIRE_IMAGE_DIGEST=true` | API | refuse images without `@sha256:<64hex>` | <!-- claim-row: Image-registry allow-list and digest verification. -->
| `CONTAINARIUM_VERIFY_IMAGE_DIGEST=true` | API | verify digest against the registry index (pre- + post-pull) |
| `CONTAINARIUM_ALLOWED_IMAGE_REGISTRIES` | API | restrict which simplestreams remotes the daemon will pull from |
| `CONTAINARIUM_KMS_BACKEND={none,inproc,vault,gcp}` | Secrets | envelope-encrypt DEKs through an external KMS |
| `CONTAINARIUM_REQUIRE_ENVELOPE=true` | Secrets | refuse legacy master-key-only rows (Phase E retirement gate) |
| `CONTAINARIUM_POSTGRES_URL_FILE` / `_PASSWORD_FILE` | Secrets | DB creds from disk rather than env |
| `CONTAINARIUM_WAKE_TRUSTED_PROXIES` | Sentinel | source-IP allowlist for `/wake/` |
| `OTEL_BEARER_REQUIRED=true` | Telemetry (collector-side; set on the OTel collector, not the daemon) | collector rejects un-bearered OTLP submissions |

---

## How it's different

### vs. SaaS-only sandboxes (e2b, Modal, Replit)

These give you sandboxes for AI agents, but only as hosted SaaS:

- **Self-hostability**: Containarium runs on your own infrastructure
  (a $5 VM, your homelab, your enterprise data center). e2b, Modal,
  and Replit are SaaS-only — your code, your data, and your customers
  go through their compute.
- **License**: Apache 2.0, no CLA. Fork it, sell it, run it.
- **Surface**: full Linux containers with `systemd`, real network
  namespaces, GPU passthrough. Not a process-per-call sandbox.
- **Transport**: MCP-native from day one, not a custom SDK with MCP
  bolted on.

### vs. Docker AI Sandboxes (`sbx`)

Docker's `sbx run claude` and Containarium both call themselves
"AI sandboxes," but they sit on opposite ends of the same spectrum:

- **Locality**: `sbx` runs the sandbox on the developer's laptop
  (microVM, host-isolation). Containarium runs the sandbox on a VM
  you host (LXC, multi-tenant, public-internet reachable via the
  sentinel).
  <!-- claim-exempt: describes another product's locality, not a Containarium claim -->
- **Persistence**: `sbx` is session-shaped (workspace mount, no
  documented "give me a box that survives reboot and has a
  hostname"). Containarium containers persist indefinitely, with
  ZFS snapshots and 30-day retention.
- **Public reach**: `containarium expose-port alice --domain
  blog.example.com` is one verb. `sbx` is laptop-local; no
  public-hostname story.
- **Agent surface**: `sbx` is CLI-first (`sbx run <agent>`).
  Containarium is MCP-native — two MCP servers (in-the-box
  `agent-box` + platform `mcp-server`) plus the same surface via
  CLI, SSH, REST/gRPC, and a web UI.
- **License**: `sbx` CLI is free; team policy (Docker Admin Console)
  is a paid subscription. Containarium is Apache 2.0 — including
  the audit log, RBAC, KMS integrations, and everything else on
  this page.

If you're stopping an agent from `rm -rf`-ing the laptop it's
running on, `sbx` is the lighter tool. If you're giving your agent
(or your customer's agent) a persistent Linux box on the public
internet, Containarium is the shape.

### vs. OSS Kubernetes agent runtimes (agent-sandbox, OpenShell)

[`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox)
and [`NVIDIA/OpenShell`](https://github.com/NVIDIA/OpenShell) are the
closest open-source peers on Kubernetes:

- **SSH-native vs. exec-based**: both agent-sandbox and OpenShell reach
  the sandbox via `kubectl exec` or a proprietary client, which requires
  the agent to hold a kube-apiserver token or cluster credentials.
  Containarium reaches the pod over SSH through sshpiper — the agent has
  no path to the cluster control plane at all.
- **MCP-native**: agent-sandbox and OpenShell expose REST APIs or custom
  SDKs. Containarium's `agent-box` MCP server runs *inside* the box,
  reachable over SSH stdio — any MCP-speaking agent (Claude Code, Cursor,
  OpenCode) works with zero client library.
- **LXC + K8s, one CLI**: Containarium runs on either Incus/LXC or
  Kubernetes behind the same `containarium` CLI and `--runtime` flag. You
  switch backends without changing anything for the agent.
- **eBPF egress policy**: Containarium can enforce per-tenant egress
  allowlists at the kernel level via TC_INGRESS eBPF programs once an
  operator arms them (opt-in; see [`docs/security/CLAIMS.md`](docs/security/CLAIMS.md)).
  agent-sandbox has NetworkPolicy; OpenShell has eBPF but is
  NVIDIA-stack-specific. Neither offers a portable, per-tenant eBPF
  allowlist across LXC and K8s backends.

### vs. dev environment platforms (Codespaces, Gitpod, Coder)

Those are persistent IDEs. Containarium is a persistent **box** —
agent-driven, not developer-driven, no IDE assumption, SSH-as-the-API:

- Containarium environments are reached by SSH and MCP. Any IDE works
  (Vim, JetBrains Remote, VS Code Remote, Cursor's remote dev — your
  call).
- Cost: no per-hour billing in the OSS path. Self-host costs are just
  your underlying VM.
- Persistence: containers survive indefinitely; Codespaces auto-delete
  after inactivity.

### vs. application container platforms (Docker, Kubernetes)

LXC is a **system** container, not an application container. Each
container has `systemd`, a real init, real users, real package managers,
real `sudo`. You can run Docker *inside* a Containarium container; the
reverse isn't really a thing.

If your agent is going to `apt install` half a Linux distro, edit
config files in `/etc`, run a database, and reboot — LXC is the right
shape. If your agent runs a single Python process, Docker or Modal is
fine.

It isn't either/or: Containarium can run a box *as a pod* in a
Kubernetes cluster you already operate — same SSH-native agent contract,
no kube-apiserver token in the agent's hands. Switch with
`--runtime=k8s`. See the **Kubernetes backend** section below.

---

## What's in the box

Beyond the agent-native primitives, Containarium ships:

### Multi-OS

- **Ubuntu 24.04 LTS** (default)
- **Rocky Linux 9** (dev/test)
- **RHEL 9** (production)
- **Windows Server VMs** via QEMU/KVM with RDP — see
  [docs/WINDOWS-VM-SETUP.md](docs/WINDOWS-VM-SETUP.md)

### GPU passthrough

For ML/AI agent workflows. Works with NVIDIA RTX 3090, RTX 4090, and
similar. PCI-level passthrough so the container sees the GPU directly.
Tested on bare-metal GPU nodes connected to the sentinel via tunnel.

### Multi-backend

A single sentinel can front:

- **GCP spot VMs**: cost-effective cloud backends with auto-recovery
  on preemption.
- **Bare-metal GPU nodes**: any Linux box you can SSH to; reaches the
  sentinel via outbound tunnel.
- **Windows VMs**: live alongside Linux backends.

All containers from all backends appear in a single unified API.

### Kubernetes backend (experimental)

Beyond the LXC/Incus backend, Containarium can run a box as a **pod in a
Kubernetes cluster you already operate** — reached over SSH exactly like
an LXC box, so an agent can't tell which substrate it landed on. The
daemon reconciles a per-tenant namespace + StatefulSet + headless Service
+ default-deny NetworkPolicy, and programs the sshpiper gateway (the
`Pipe` CRD) so `ssh <tenant>@<gateway>` routes to the right pod.
What the NetworkPolicy does and does not guarantee is in
[`docs/security/CLAIMS.md`](docs/security/CLAIMS.md).
<!-- claim-row: Kubernetes boxes get a default-deny NetworkPolicy. -->

The pitch isn't "another way to run pods" — it's giving an agent a
hardened, SSH-native foothold in your cluster **without handing it a
kube-apiserver token**: the box runs with `automountServiceAccountToken:
false` and satisfies the `restricted` Pod Security profile. Compare this
to `kubectl exec`-based runtimes where the agent necessarily holds cluster
credentials.

**Runtime selection** (no recompile needed):

```bash
# LXC/Incus (default)
containariumd daemon

# Kubernetes — uses in-cluster config or KUBECONFIG
CONTAINARIUM_RUNTIME=k8s containariumd daemon
# or
containariumd daemon --runtime=k8s
```

Both backends share the same CLI, MCP tools, JWT auth, and REST/gRPC API.
GPU passthrough is supported on K8s via `nvidia.com/gpu` resource limits.
Local bring-up takes under 5 minutes with `kind` — see
[docs/KIND-QUICKSTART.md](docs/KIND-QUICKSTART.md).

Design, topology, and BYO-cluster integration:
[docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md](docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md).

### Web UI

A basic dashboard at `/webui/` for users who'd rather not type CLI:
container list, lifecycle controls, metrics, browser-based terminal.
Polished UI is intentionally a cloud-product concern — the OSS web UI
is functional, not opinionated.

### Persistent storage (ZFS)

Containers survive VM restarts and spot termination. ZFS handles
compression, snapshots (daily by default, 30-day retention), and
checksums.

### Sentinel HA

The sentinel itself is e2-micro (free tier). It:

- Detects spot preemption in ~10s, serves a maintenance page.
- Restarts spot VMs automatically (~85s total recovery).
- Holds the static IP, so DNS doesn't change as backends rotate.

### Monitoring & observability

VictoriaMetrics + Grafana auto-provisioned. Per-container CPU,
memory, disk, network. Alerting via webhooks. SSH audit logs per
user (LXC backend; the Kubernetes backend records control-plane API
audit but not in-box SSH sessions — see #1189).
<!-- claim-row: SSH session audit logs per user. -->

### Security primitives

- **Unprivileged LXC containers**: container root ≠ host root.
- **Per-user proxy accounts**: `/usr/sbin/nologin` on the sentinel,
  users can only proxy through to their container.
- **fail2ban per-user**: an attack on Alice's account doesn't ban
  Bob.
- **Trivy** image/dependency scanning across backends. **ClamAV** malware scanning is a tenant-run capability (the scan runs inside a box, started by its owner, and the verdict is self-reported); it is being rebuilt in #2631 and is not a platform-wide control today.
- **AppArmor profiles** per container.
- **AGENTBOX_ROOT sandbox** to constrain agent-box file ops at runtime.

**Zero-trust controls** (see [`docs/security/OPERATOR-SECURITY-RUNBOOK.md`](docs/security/OPERATOR-SECURITY-RUNBOOK.md); several are opt-in, see [`docs/security/CLAIMS.md`](docs/security/CLAIMS.md) for what is on by default):

- **JWT with `iss` / `aud` / `jti` / `tt` / `scopes`**: 32-byte minimum
  secret enforced at startup; refresh tokens are single-use; jti-based
  revocation; per-tool MCP scopes propagate to server-side gates.
- **Admin RBAC + per-container ownership** on the API surface; cluster
  ops admin-only, container ops owner-only.
- **KMS envelope encryption for tenant secrets** (Vault Transit or GCP
  Cloud KMS), with a migration tool and master-key retirement gate.
- **tmpfs `--delivery=file`** for secrets that shouldn't be visible in
  `/proc/<pid>/environ`.
- **Audit log with SHA-256 hash chain** + `containariumd audit verify`
  (on the host): tamper-evident against an application- or database-level
  attacker. An attacker who can write the database can rewrite the chain
  consistently; the chain root can be anchored to a file outside the database
  (`internal/audit/anchor.go`), which a database credential does not reach, but
  a host-root attacker could still edit both.
- **Image-registry allowlist + pre-pull simplestreams digest
  verification + post-pull `volatile.base_image` defense-in-depth**
  for supply-chain hardening.
- **`SECURITY.md`** with a 90-day coordinated-disclosure window;
  `gosec` / `govulncheck` / `trivy` running in CI.

---

## CLI reference (essentials)

### Container lifecycle

```bash
# Create (Ubuntu 24.04, default)
containarium create alice --ssh-key ~/.ssh/id_ed25519.pub

# Create with options
containarium create ml-dev \
  --ssh-key ~/.ssh/id_ed25519.pub \
  --gpu 0 \
  --stack gpu \
  --memory 16GB \
  --cpu 4

# Lifecycle
containarium list
containarium info
containarium wake alice     # start a stopped box
containarium sleep alice    # stop it
containarium delete alice
```

### Networking

```bash
# Expose a container port on a public hostname
containarium expose-port alice \
  --container-port 8080 \
  --domain blog.example.com

# Lower-level route management
containarium route add api.example.com --target 10.0.3.42:3000
containarium route list
containarium route delete api.example.com

# Raw TCP/UDP passthrough via local iptables (on the host, so it's a
# containariumd verb)
containariumd passthrough add --port 50051 \
  --target-ip 10.0.3.150 --target-port 50051

# Raw TCP/UDP passthrough via the daemon API (works against a remote box)
containarium passthrough-route add --port 9443 \
  --target-ip 10.0.3.150 --target-port 50051 --server <host:port>
containarium passthrough-route list --server <host:port>
containarium passthrough-route remove --port 9443 --server <host:port>
```

### SSH config

```bash
# Print to stdout (preview)
containarium ssh-config show

# Write to ~/.containarium/ssh_config (one-line `Include` to wire in)
containarium ssh-config sync
containarium ssh-config sync --sentinel sentinel.example.com  # via sentinel
containarium ssh-config sync --identity ~/.ssh/containarium_ed25519

# From a client machine, against a remote server (the bare form above
# only reaches a local Incus socket)
containarium ssh-config sync --http --server <host:port>
```

### Authentication

```bash
# Issue an access + refresh pair (CLI-only; never exposed via API).
# Access tokens are short-lived (default 15 min) and authenticate the
# API. Refresh tokens are long-lived and single-use — exchange via
# POST /v1/tokens/refresh for a new pair.
containarium token generate \
  --username admin \
  --roles admin \
  --secret-file /etc/containarium/jwt.secret

# Use the access token
curl -H "Authorization: Bearer <access-token>" http://localhost:8080/v1/containers

# Exchange the refresh token for a new pair (same as POST /v1/tokens/refresh;
# the refresh token is the credential, so no --token)
containarium token refresh --refresh-token "$REFRESH" --server <host:port>

# Inspect a token's claims (jti, scopes, expiry, validation)
containarium token inspect <token> --secret-file /etc/containarium/jwt.secret

# Revoke a leaked token by jti (idempotent; reads from `audit query`
# or `token inspect`)
containarium token revoke <jti> --reason "leak_2026_05_22"
containarium token list-revoked

# Mint a least-privilege token for an agent with only the scopes it
# needs — server-side gates enforce this even if the agent ignores
# the filter.
containarium token generate \
  --username alice-agent \
  --scopes containers:read,containers:write \
  --secret-file /etc/containarium/jwt.secret
```

See [`docs/security/OPERATOR-SECURITY-RUNBOOK.md`](docs/security/OPERATOR-SECURITY-RUNBOOK.md)
for the full token lifecycle, leak-response playbook, and the agent
least-privilege scope catalog.
<!-- claim-exempt: points to the runbook, asserts no property of the product -->

---

## Deployment

### Two binaries: `containarium` vs `containariumd`

Same convention as `docker`/`dockerd` or `incus`/`incusd`: `containarium`
is the CLI you run from your laptop (`create`, `list`, `ssh-config`,
`expose-port`, …); `containariumd` is what runs *on the host* — the
daemon, the sentinel, and every on-host admin verb (`daemon`,
`sentinel`, `service install`, `sync-accounts`, `pool join`, `node`,
`hosting`, `recover`, `doctor`). Install `containarium` wherever you
want to drive the platform from (your laptop, a jump box); install
`containariumd` on every host that runs the daemon or sentinel itself.
A compat symlink (`/usr/local/bin/containarium -> containariumd`) keeps
