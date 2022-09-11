package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/gatewayprovider"
	"github.com/footprintai/containarium/internal/sshkey"
	"github.com/footprintai/containarium/pkg/core/sdlcskills"
	"github.com/footprintai/containarium/pkg/version"
	"github.com/spf13/cobra"
)

// `containarium code` (#1673, #1674) is the box-side coding-agent surface:
// `install` lands the toolchain on a box you already use (PRD Story 2,
// docs/product/remote-coding-agent.md); `run`/`attach`/`status`/`stop` are
// the resumable reader over agent-box's tail_log/process_start/process_kill
// (PRD Story 3, Part B of the design doc) — it feels like running the agent
// in a pipe, but survives the local client's network dying mid-run. See
// internal/coderun for the resumable-reader core `run`/`attach` wrap.
//
// Box resolution/SSH-key authorization is shared by all five subcommands
// and deliberately mirrors connect.go's runConnect: same connectAPI, same
// obtainConnectKey, same connectcore helpers — this package is the second
// caller of that pattern (after `connect`), which is why obtainConnectKey
// takes explicit params instead of reading connect's own package vars.

// `code install` carries NO credential (#2030). Claude Code's terms
// (https://code.claude.com/docs/en/legal-and-compliance, "Authentication and
// credential use") forbid a platform collecting, storing, or intermediating a
// Claude.ai credential or session token: sign-in must complete through
// Anthropic's own flow. The permitted lane for hosting Claude Code in a
// sandbox is the unmodified binary with each end user bringing their own
// credential — so this command installs a toolchain and stops there. It
// neither reads, lists, nor names a Claude.ai token.

// claudeInstallScript and codeInstallStateFile moved to
// internal/coderun/engine with the rest of Claude Code's engine behaviour
// (#1727). They are ALIASED rather than re-declared so there is exactly one
// definition: two copies of an installer line is how the CLI and the engine
// drift, and #2030's tests assert on these names.
const claudeInstallScript = engine.ClaudeInstallScript

// agentBoxRepo is where the agent-box / mcp-server release assets live. Same
// assets scripts/install-agent-runtime.sh pulls for the agent-runtime recipe
// — `code install` lands them on an ordinary box so code run/attach/status/
// stop work there too, instead of only on a recipe box.
const agentBoxRepo = "FootprintAI/Containarium"

// agentBoxAssetArch is the only Linux build the release publishes
// (Makefile's build-agent-box-all). Boxes are Linux; darwin assets exist for
// laptops, not for this path.
const agentBoxAssetArch = "linux-amd64"

// The install-state file that carries "did a credentials file exist before the
// install?" from the install step to the verify step now lives with the rest of
// Claude Code's engine behaviour, as engine.ClaudeInstallStateFile (#1727).

// defaultCodeRunName is the process name used when --name is omitted, so
// the common case ("one coding task per box at a time") never requires the
// user to track an arbitrary generated name across run/attach/status/stop.
const defaultCodeRunName = coderun.DefaultRunName

// codeStreamFollow bounds how long each tail_log call blocks server-side
// polling for new content — most of the "streaming" feel comes from this,
// not from client-side poll frequency. Short enough that the post-exit
// grace period (codeStreamFollow + codeExitPollInterval) stays well under
// what a human notices as a delay before the command returns.
const codeStreamFollow = 3 * time.Second

// codeExitPollInterval is how often run/attach checks process_list for the
// run's own exit, independent of the tail_log streaming loop.
const codeExitPollInterval = 1 * time.Second

var (
	codeSSHServer string
	codeKeyPath   string
	codeIdentity  string
	codeUser      string
	codeHost      string
	codePort      int
	codeName      string
	codeKeyWait   string

	// `code install` only (#2030).
	codeBootstrapURL      string
	codeNoSDLCSkills      bool
	codeSkillSets         string
	codeRelease           string
	codeClaudeCodeVersion string

	// `code install` engine + credential selection (#1727).
	codeEngine          string
	codeCredential      string
	codeProvider        string
	codeSecretName      string
	codeProviderBaseURL string
	codeModel           string
	codePiVersion       string
	codeCodexVersion    string
)

// Test seams. Production never reassigns these; they exist so the install
// flow can be driven end to end without a box, a daemon, or an ssh binary.
var (
	sshExec             = runSSHCaptured
	resolveCodeTargetFn = resolveCodeTarget
	// mintGatewayTokenFn is the ModelGatewayService mint call (#1726), behind a
	// seam so the install and run paths are testable without a daemon. #1727
	// CONSUMES that RPC and does not modify it.
	mintGatewayTokenFn = mintGatewayTokenViaClient
	// listSecretsFn is the metadata-only secrets read the `secret` credential
	// source's preflight uses. Metadata only: name and delivery mode, never a
	// value.
	listSecretsFn = listSecretMetadata
)

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Run and manage a coding agent on a box",
}

var codeInstallCmd = &cobra.Command{
	Use:   "install <box>",
	Short: "Install a coding agent onto an existing, already-provisioned box",
	Long: `Installs a coding agent onto a box you already use, over the existing SSH
path — no new box type, no daemon-side privileged exec.

--engine picks the agent; it DEFAULTS TO claude, so an invocation that worked
before this flag existed behaves exactly as it did.

  --engine claude   Claude Code (the default)
  --engine pi       pi (https://pi.dev), which runs inside the box — see
                    docs/integrations/pi.md
  --engine codex    OpenAI's Codex CLI (https://developers.openai.com/codex/cli),
                    which also runs inside the box — see docs/integrations/codex.md.
                    --credential gateway is not supported for codex yet; use
                    --credential secret.

--credential picks where the engine's model credential comes from; it defaults
to secret, again the pre-existing behaviour.

  --credential secret    the box's own environment carries a provider key,
                         delivered by the secrets store. With --secret-name the
                         install checks that secret's delivery mode is one an
                         SSH shell session can actually see (env delivery is
                         not — see docs/integrations/pi.md).
  --credential gateway   the box holds a short-lived, scoped token from the
                         containarium model gateway instead of a provider key,
                         and the real upstream key never leaves the daemon.
                         Needs --provider. Validated at install time with a
                         dry-run mint, so a missing key is named here rather
                         than at your first prompt.

Claude Code + secret carries no credential at all: Claude Code's terms require
sign-in to complete through Anthropic's own flow
(https://code.claude.com/docs/en/legal-and-compliance), so you bring your own
afterwards, one of two ways:

  interactive — SSH in and run claude, then complete the device-code sign-in
  (the documented path for SSH sessions and containers):

    containarium connect <box>
    claude

  headless — place your own ANTHROPIC_API_KEY (or a Bedrock / Vertex / Foundry
  credential) in the "env" block of ~/.claude/settings.json on the box. Both
  interactive claude and agent-box-launched runs read it.

It also lands the agent-box helper on ~/.local/bin, so containarium code
run/attach/status/stop work on this box, and records the choices above in
~/.containarium/code.json (0600) so ` + "`code run`" + ` never re-asks.

After installing it prints the binary version and reports which credential
SOURCE the box has — names only, never a value. On the Claude path it also
asserts the install created no ~/.claude/.credentials.json.

Examples:
  containarium code install alice
  containarium code install alice --engine pi --credential gateway \
      --provider kafeido --model kafeido-coder
  containarium code install alice --engine pi --credential secret \
      --secret-name OPENAI_API_KEY --provider-base-url https://api.openai.com/v1 \
      --model gpt-5
  containarium code install alice --engine codex --credential secret \
      --secret-name CODEX_API_KEY`,
	Args: cobra.ExactArgs(1),
	RunE: runCodeInstall,
}

// codeRunCmd, codeAttachCmd, codeStatusCmd, codeStopCmd and their RunE
// handlers are defined in code_run.go (the resumable-reader core, #1674) —
// declared there rather than here since they're wired into codeCmd below
// alongside codeInstallCmd (#1673).
func init() {
	rootCmd.AddCommand(codeCmd)
	codeCmd.PersistentFlags().StringVar(&codeSSHServer, "ssh-server", "", "server to talk to for box resolution / SSH-key authorization (default: --server / CONTAINARIUM_SERVER, else your logged-in server) — deliberately NOT named --server: that flag already means \"the daemon secrets are read from\", and this can legitimately differ (e.g. a cloud-fronted login server vs. a direct daemon address)")
	codeCmd.PersistentFlags().StringVar(&codeKeyPath, "key", "", "public key to authorize (default: the managed key from `containarium ssh setup`)")
	codeCmd.PersistentFlags().StringVar(&codeIdentity, "identity", "", "private key path to authenticate with (default: derived from --key)")
	codeCmd.PersistentFlags().StringVar(&codeUser, "user", "", "override the SSH username (default: the box's own user)")
	codeCmd.PersistentFlags().StringVar(&codeHost, "host", "", "override the SSH host (default: the box's sentinel host)")
	codeCmd.PersistentFlags().IntVar(&codePort, "port", 0, "override the SSH port")
	codeCmd.PersistentFlags().StringVar(&codeKeyWait, "key-wait", "", keyWaitFlagUsage)

	codeCmd.AddCommand(codeInstallCmd)
	codeCmd.AddCommand(codeRunCmd)
	codeCmd.AddCommand(codeAttachCmd)
	codeCmd.AddCommand(codeStatusCmd)
	codeCmd.AddCommand(codeStopCmd)

	// --name applies to run/attach/status/stop (which task on the box) —
	// not install, which has no notion of a named run.
	for _, c := range []*cobra.Command{codeRunCmd, codeAttachCmd, codeStatusCmd, codeStopCmd} {
		c.Flags().StringVar(&codeName, "name", "", "process name for this run (default: \""+defaultCodeRunName+"\" — override to run more than one task on the same box concurrently)")
	}

	codeInstallCmd.Flags().StringVar(&codeBootstrapURL, "bootstrap-url", "",
		"URL of a .tar.gz bundle to apply after the toolchain lands — unpacked into a temp dir on the box and its apply.sh run as the box user (skipped when empty)")
	codeInstallCmd.Flags().BoolVar(&codeNoSDLCSkills, "no-sdlc-skills", false,
		"deprecated alias for --skill-sets none")
	_ = codeInstallCmd.Flags().MarkDeprecated("no-sdlc-skills", "use --skill-sets none")
	codeInstallCmd.Flags().StringVar(&codeSkillSets, "skill-sets", "",
		"which pinned SDLC skill sets to install into ~/.claude/skills: a comma-separated list of set ids (for example sdlc,qa), \"all\", or \"none\" (default: all). An unknown id fails and names the known ones; narrowing the list on a re-run removes the dropped sets")
	codeInstallCmd.Flags().StringVar(&codeRelease, "release", "",
		"release tag to pull the agent-box / mcp-server assets from, v-prefixed (default: this CLI's own version)")
	codeInstallCmd.Flags().StringVar(&codeClaudeCodeVersion, "claude-code-version", "",
		"pin the Claude Code version the installer fetches (default: whatever the installer considers current)")

	// #1727: engine and credential source as typed choices.
	//
	// --engine DEFAULTS TO claude. That default is the compatibility contract
	// for every box and every script that predates this flag, and
	// TestCodeInstall_EngineDefaultsToClaude pins it.
	codeInstallCmd.Flags().StringVar(&codeEngine, "engine", string(engine.DefaultName),
		"coding engine to install: "+strings.Join(engine.Names(), " | "))
	codeInstallCmd.Flags().StringVar(&codeCredential, "credential", string(engine.DefaultKind),
		"where the engine's model credential comes from: "+strings.Join(engine.Kinds(), " | "))
	codeInstallCmd.Flags().StringVar(&codeProvider, "provider", "",
		"gateway provider to mint tokens for, with --credential gateway (one of: "+strings.Join(gatewayprovider.Names(), ", ")+")")
	codeInstallCmd.Flags().StringVar(&codeSecretName, "secret-name", "",
		"tenant secret holding the provider key, with --credential secret (e.g. ANTHROPIC_API_KEY)")
	codeInstallCmd.Flags().StringVar(&codeProviderBaseURL, "provider-base-url", "",
		"point the engine at this base URL instead of the gateway's (complete URL; no per-provider suffix is added)")
	codeInstallCmd.Flags().StringVar(&codeModel, "model", "",
		"model id to pin runs to; also the gateway token's allowed_models ceiling")
	codeInstallCmd.Flags().StringVar(&codePiVersion, "pi-version", engine.PiVersion,
		"pin the pi version installed with --engine pi")
	codeInstallCmd.Flags().StringVar(&codeCodexVersion, "codex-version", "",
		"pin the Codex CLI version installed with --engine codex (default: whatever npm considers current)")

	codeRunCmd.Flags().StringVar(&codeRunPrompt, "prompt", "", "prompt to give the agent (required)")
	codeRunCmd.Flags().BoolVar(&codeRunContinue, "continue", false,
		"resume the engine's most recent session in the run's working directory instead of starting a fresh one (`pi -c`, `claude --continue`)")
	codeRunCmd.Flags().StringVar(&codeRunSession, "session", "",
		"resume this specific session id instead of starting a fresh one (`claude --resume <id>`, `pi --session <id>`) — mutually exclusive with --continue; also becomes this run's --name unless --name is given explicitly")
	codeRunCmd.Flags().StringVar(&codeRunTokenTTL, "token-ttl", "",
		"lifetime of the gateway token minted for this run (e.g. 2h); capped server-side. Default 24h")
	codeRunCmd.Flags().StringVar(&codeRunApprovals, "approvals", string(coderun.ApprovalsDeny),
		"what a tool call that needs a permission does: deny (refused, today's behaviour) or ask (the run pauses until the request is answered or expires into a deny; Claude Code only)")
	codeRunCmd.Flags().DurationVar(&codeRunApprovalTimeout, "approval-timeout", coderun.DefaultApprovalTimeout,
		"with --approvals ask, how long a permission request waits before it expires into a deny")
	codeRunCmd.Flags().BoolVar(&codeRunStreamJSON, "output-format-stream-json", false,
		"capture stdout/stderr separately (framed capture_mode) so a JSON stream on stdout isn't corrupted by diagnostics; without this, both are interleaved as plain text")
	codeAttachCmd.Flags().BoolVar(&codeAttachStreamJSON, "output-format-stream-json", false,
		"demultiplex stdout/stderr — must match the capture_mode the run was actually started with")
	codeStopCmd.Flags().BoolVar(&codeStopForce, "force", false, "SIGKILL instead of SIGTERM")
}

func codeRunName() string {
	if codeName != "" {
		return codeName
	}
	// #2193: resuming a specific session with no explicit --name uses the
	// session id as the run name too — the same name attach/status/stop
	// already need to find this run by, and what the daemon's StartBoxRun
	// does for the same reason (a resumed session's collision check is keyed
	// on its own id, not on the shared "code" default every fresh run uses).
	if codeRunSession != "" {
		return codeRunSession
	}
	return defaultCodeRunName
}

// resolveCodeTarget authorizes the caller's managed SSH key on box (same
// flow as `containarium connect`) and returns the resolved SSH target plus
// the private key path to authenticate with. Shared by every code
// subcommand; run/attach/status/stop layer an MCP session on top
// (resolveCodeSession), `install` execs a plain script directly.
func resolveCodeTarget(ctx context.Context, box string, diag io.Writer) (connectcore.Target, string, error) {
	if err := validateBoxName(box); err != nil {
		return connectcore.Target{}, "", err
	}

	sshServer := codeSSHServer
	if sshServer == "" {
		sshServer = serverAddr
	}
	server := pickSSHServer(sshServer)
	api, err := newConnectAPI(server)
	if err != nil {
		return connectcore.Target{}, "", err
	}

	c, err := api.GetContainer(ctx, box)
	if err != nil {
		return connectcore.Target{}, "", err
	}
	if !connectcore.IsRunning(c.State) {
		return connectcore.Target{}, "", fmt.Errorf("box %q is %s, not running — start it first (`containarium wake %s`)",
			box, connectcore.PrettyState(c.State), box)
	}
	target, err := connectcore.BuildTarget(c, codeUser, codeHost, codePort)
	if err != nil {
		return connectcore.Target{}, "", err
	}

	pub, privPath, err := obtainConnectKey(codeKeyPath, codeIdentity)
	if err != nil {
		return connectcore.Target{}, "", err
	}
	if err := api.AuthorizeKey(ctx, box, pub); err != nil {
		return connectcore.Target{}, "", fmt.Errorf("authorize key on %q: %w", box, err)
	}
	fp, _ := sshkey.Fingerprint(pub)
	fmt.Fprintf(diag, "✓ %s → %s@%s (authorized %s)\n", box, target.User, target.Host, fp)

	return target, privPath, nil
}

// resolveCodeSession resolves box (resolveCodeTarget) and opens an MCP
// session to its agent-box over that SSH path — the transport
// run/attach/status/stop need. `install` doesn't call this: it execs a
// plain shell script over SSH directly, no MCP involved.
func resolveCodeSession(ctx context.Context, box string, diag io.Writer) (*coderun.Session, error) {
	target, privPath, err := resolveCodeTarget(ctx, box, diag)
	if err != nil {
		return nil, err
	}
	// resolveCodeTarget just authorized the key: armed (#2013).
	kw, err := newKeyWait(codeKeyWait, true, diag)
	if err != nil {
		return nil, err
	}
	sshArgs := connectcore.BuildSSHArgs(target, privPath, "") // no remote command — Connect appends "agent-box"
	sess, err := connectCodeSession(ctx, kw, probeSSHArgs(sshArgs), func(ctx context.Context) (*coderun.Session, error) {
		return coderun.Connect(ctx, sshArgs)
	})
	if err != nil {
		if errors.Is(err, connectcore.ErrKeyNotLearned) {
			return nil, fmt.Errorf("connect to agent-box on %q: %w", box, err)
		}
		if errors.Is(err, coderun.ErrAgentBoxMissing) {
			return nil, coderun.AgentBoxMissingError(box)
		}
		return nil, fmt.Errorf("connect to agent-box on %q: %w", box, err)
	}
	return sess, nil
}

// connectCodeSession opens the agent-box session under kw. A failed MCP
// dial doesn't say WHY ssh failed (and a publickey denial otherwise looks
// like "agent-box missing"), so on failure one silent probe classifies it:
// publickey-denied is retried while the sentinel learns the key; anything
// else returns the dial's own error unchanged.
func connectCodeSession(ctx context.Context, kw connectcore.KeyWait, probe []string, dial func(context.Context) (*coderun.Session, error)) (*coderun.Session, error) {
	var sess *coderun.Session
	err := kw.Do(ctx, func(ctx context.Context, attempt int) error {
		if attempt > 1 {
			if perr := sshProbeFn(ctx, probe); perr != nil {
				return perr
			}
		}
		s, err := dial(ctx)
		if err == nil {
			sess = s
			return nil
		}
		if kw.Armed && kw.Window > 0 {
			if perr := sshProbeFn(ctx, probe); connectcore.IsPublickeyDenied(perr) {
				return perr
			}
		}
		return err
	})
	return sess, err
}

// streamAndWait streams path's output to stdout (demultiplexed to
// stdout+stderr when streamJSON) until ctx is cancelled or name's run has
// exited, polling process_list independently of the tail_log loop to
// notice the exit. After noticing, it waits one more codeStreamFollow
// cycle so a last in-flight read can flush trailing output before
