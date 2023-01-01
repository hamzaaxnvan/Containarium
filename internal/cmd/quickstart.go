package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/sshkey"
	"github.com/spf13/cobra"
)

// quickstart collapses the README quick-start into one laptop-side command:
//
//  1. create a box                 (runCreate; remote via --server, or local)
//  2. write ~/.containarium/ssh_config + wire the Include line into
//     ~/.ssh/config                (runSSHConfigSync + ensureSSHInclude)
//  3. merge the agent-box entry into the caller's agent MCP config
//     (claude/gemini/codex)                          (agentSpec.wireMCP)
//  4. [--prompt] expose the URL, then LAUNCH the caller's own agent
//     on the build task                              (launchAgent)
//
// Topology (pure BYOA — decided): the LLM + the API key live on the LAPTOP.
// The box is a fresh Ubuntu container running agent-box (an MCP *server* — the
// hands, no brain). So --prompt cannot "seed a file and hope"; nothing in the
// box would read it. Instead quickstart runs on the laptop and, for --prompt,
// execs the laptop's own agent, which step 3 has already wired to the box over
// MCP. The agent (brain, on laptop) drives agent-box tools (hands, in box).
//
// Consequently quickstart is a LAPTOP command, not the install.sh one-liner:
// the SSH wiring, the MCP merge, and the agent launch all configure the user's
// own machine. The `curl … | sudo bash` install path bootstraps the daemon (+
// optionally a first box) on the server and stops there — it has no agent and
// no key to launch, and the human isn't on that machine.
//
// It reuses the existing subcommands' RunE functions by setting their
// package-level flag vars — quickstart is orchestration, not new client code.
// Every step is idempotent so a re-run heals a partial setup.
var (
	qsSSHKeyPath  string
	qsStack       string
	qsCPU         string
	qsMemory      string
	qsSentinel    string
	qsPrompt      string
	qsDomain      string
	qsExposePort  int
	qsAgent       string
	qsAgentName   string
	qsSkipMCP     bool
	qsSkipInclude bool
	qsNoLaunch    bool
	qsKeyWait     string
)

var quickstartCmd = &cobra.Command{
	Use:   "quickstart <name>",
	Short: "Create a box, wire SSH + your agent, and (optionally) build a site — in one step",
	Long: `Quickstart runs the whole README quick-start as a single idempotent command.

This is a LAPTOP command: it configures your machine (SSH + your agent's MCP
config) and, with --prompt, launches your local agent. It talks to a remote
daemon via --server, or drives a local Incus in single-machine dev.

    containarium quickstart alice --ssh-key ~/.ssh/id_ed25519.pub \
      --server vm.example.com

What it does, in order:
  1. create the box (no-op if it already exists)
  2. sync ~/.containarium/ssh_config AND add the one Include line to
     ~/.ssh/config for you (the manual edit the README asks for)
  3. merge a "containarium-box" MCP server into your agent's config
     (claude → ~/.claude.json, gemini → ~/.gemini/settings.json,
     codex → ~/.codex/config.toml) — so your agent reaches the box with
     no copy-paste
  4. print the exact next action

Build a site in the same step (--prompt):

    containarium quickstart alice --ssh-key ~/.ssh/id_ed25519.pub \
      --server vm.example.com \
      --prompt "a landing page for my coffee shop, dark theme" \
      --domain coffee.example.com --agent claude

--prompt exposes --domain, then launches YOUR local agent (--agent: claude,
gemini, or codex) on the task. The agent runs on your laptop with your key;
step 3 has already wired it to the box, so it builds by driving the box's
agent-box tools over MCP and serves on the exposed port — the public URL is
live when it's done. OSS ships no model key: the brain is always your agent,
on your laptop.

Note: the fresh-VM installer (curl … | sudo bash) only bootstraps the daemon.
The build step lives here, on the laptop where your agent and key are.`,
	Args: cobra.ExactArgs(1),
	RunE: runQuickstart,
}

func init() {
	rootCmd.AddCommand(quickstartCmd)

	quickstartCmd.Flags().StringVar(&qsSSHKeyPath, "ssh-key", "", "Path to SSH public key to seed into the box. Optional: if omitted, quickstart reuses your existing ~/.ssh key or generates a managed one (containarium_ed25519) — the private key stays on your laptop.")
	quickstartCmd.Flags().StringVar(&qsStack, "stack", "fullstack", "Software stack to install in the box (default fullstack so a web build has node/npm ready)")
	quickstartCmd.Flags().StringVar(&qsCPU, "cpu", "4", "CPU cores for the box")
	quickstartCmd.Flags().StringVar(&qsMemory, "memory", "4GB", "Memory for the box")
	quickstartCmd.Flags().StringVar(&qsSentinel, "sentinel", "", "Sentinel SSH endpoint for ssh_config routing (empty = direct mode)")

	quickstartCmd.Flags().StringVar(&qsPrompt, "prompt", "", "What to build. Exposes --domain and launches your local --agent on the task (BYOA — your agent, your key, on your laptop).")
	quickstartCmd.Flags().StringVar(&qsDomain, "domain", "", "Public hostname to expose the build on (required with --prompt unless --expose-port is 0). DNS for it must point at the sentinel.")
	quickstartCmd.Flags().IntVar(&qsExposePort, "expose-port", 8080, "Container port to expose for the build (0 disables the pre-expose)")

	quickstartCmd.Flags().StringVar(&qsAgent, "agent", "claude", "Which local agent to wire + launch for --prompt: claude, gemini, or codex. Must be installed on your laptop.")
	quickstartCmd.Flags().BoolVar(&qsNoLaunch, "no-launch", false, "With --prompt, do everything but launching the agent — print the command to run instead")

	quickstartCmd.Flags().StringVar(&qsAgentName, "agent-name", "containarium-box", "Name of the MCP server entry written into agent configs")
	quickstartCmd.Flags().BoolVar(&qsSkipMCP, "no-mcp", false, "Skip writing any agent MCP config")
	quickstartCmd.Flags().BoolVar(&qsSkipInclude, "no-ssh-include", false, "Skip appending the Include line to ~/.ssh/config (still writes ~/.containarium/ssh_config)")
	quickstartCmd.Flags().StringVar(&qsKeyWait, "key-wait", "", keyWaitFlagUsage)
}

// Seams: the side-effecting steps (daemon RPCs + the agent exec) are indirected
// through function vars so an integration test can substitute fakes and drive
// the whole orchestration — step order, flag wiring, file writes, the launched
// argv — without a daemon or a real agent. Production uses the real functions.
var (
	qsStepCreate      = runCreate
	qsStepSSHConfig   = runSSHConfigSync
	qsStepExposePort  = runExposePort
	qsStepLaunchAgent = launchAgent
	qsStepWaitForKey  = waitQuickstartKey
)

func runQuickstart(cmd *cobra.Command, args []string) error {
	name := args[0]

	if _, ok := agentSpecs[qsAgent]; !ok {
		return fmt.Errorf("unknown --agent %q (supported: %s)", qsAgent, strings.Join(supportedAgents, ", "))
	}

	// Resolve the human's home even when running under sudo (local Incus dev
	// needs root). ssh_config, ~/.ssh/config and the MCP configs all belong to
	// the invoking user, NOT root — otherwise `ssh alice` and the agent wiring
	// silently land in /root and do nothing. Single biggest footgun; handle
	// it once here.
	home, uid, gid, err := invokingUserHome()
	if err != nil {
		return fmt.Errorf("resolve invoking user home: %w", err)
	}

	// Skip bring-your-own-ssh-key: with no --ssh-key, reuse the user's
	// existing ~/.ssh key or generate a managed one. The private key never
	// leaves the laptop; sshIdentity feeds ssh_config's IdentityFile so
	// `ssh <box>` works without the user touching a key at all.
	pubKey, sshIdentity, generatedKey, err := resolveSSHKey(home, qsSSHKeyPath)
	if err != nil {
		return fmt.Errorf("resolve SSH key: %w", err)
	}

	launching := qsPrompt != "" && !qsNoLaunch
	if qsPrompt != "" {
		if qsDomain == "" && qsExposePort != 0 {
			return fmt.Errorf("--prompt needs a public URL: pass --domain (or --expose-port 0 to skip pre-exposing)")
		}
		if launching {
			// Fail fast, BEFORE creating a box, if the agent binary isn't here
			// — the whole point of --prompt is that the launch lands.
			if _, err := resolveAgent(qsAgent); err != nil {
				return err
			}
		}
	}

	fmt.Printf("▶ quickstart %q\n\n", name)

	// ── Step 1: create ──────────────────────────────────────────────────
	// Reuse runCreate by populating its flag vars. --force stays off so a
	// re-run is a no-op on an existing box rather than a destructive recreate;
	// swallow the "already exists" error to stay idempotent.
	fmt.Println("① create box")
	if qsSSHKeyPath == "" {
		chownUnderHome(pubKey, uid, gid)
		chownUnderHome(sshIdentity, uid, gid)
		if generatedKey {
			fmt.Printf("  generated managed SSH key %s\n", sshIdentity)
		} else {
			fmt.Printf("  using existing SSH key %s\n", pubKey)
		}
	}
	sshKeyPath = pubKey
	stackID = qsStack
	cpuLimit = qsCPU
	memoryLimit = qsMemory
	enablePodman = true
	created := true
	if err := qsStepCreate(cmd, args); err != nil {
		if !isAlreadyExists(err) {
			return fmt.Errorf("create: %w", err)
		}
		created = false
		fmt.Printf("  box %q already exists — reusing\n", name)
	}

	// ── Step 2: ssh_config + Include ────────────────────────────────────
	fmt.Println("\n② wire SSH")
	sshConfigSentinel = qsSentinel
	sshConfigIdentity = sshIdentity // "" when the user brought their own key
	sshConfigOutPath = filepath.Join(home, ".containarium", "ssh_config")
	if err := qsStepSSHConfig(cmd, nil); err != nil {
		return fmt.Errorf("ssh-config sync: %w", err)
	}
	chownUnderHome(sshConfigOutPath, uid, gid)

	if !qsSkipInclude {
		added, err := ensureSSHInclude(filepath.Join(home, ".ssh", "config"), sshConfigOutPath, uid, gid)
		if err != nil {
			return fmt.Errorf("wire Include into ~/.ssh/config: %w", err)
		}
		if added {
			fmt.Printf("  added Include line to %s\n", filepath.Join(home, ".ssh", "config"))
