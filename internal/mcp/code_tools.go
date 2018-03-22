package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/sshkey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Platform-MCP wrappers for `containarium code` (#1698). Every operation here
// is the SAME internal/coderun function the CLI handler calls — CLAUDE.md's
// CLI-first rule, which had been running one-directional: the verb shipped in
// #1673/#1674 with no MCP counterpart, so an agent could create a box and
// expose a port but could not start a coding run on one or reattach to it.
//
// The streaming shape differs by necessity. The CLI streams to a terminal
// until interrupted; MCP is request/response, so `code_run` and `code_attach`
// return a BOUNDED window plus the offset to resume from, mirroring tail_log's
// existing contract. An agent that carries `next_offset` across calls gets
// exactly the disconnect-tolerant behavior a human gets from the CLI.

// codeReadWindow bounds how long one code_run/code_attach call waits for new
// output. Long enough that a caller polling in a loop sees steady progress,
// short enough that no single MCP call looks hung.
const codeReadWindow = 5 * time.Second

// mcpCodeSession resolves box and opens an MCP session to its agent-box over
// SSH — the same transport `connect` uses, so key/certificate handling stays
// in one place rather than being reimplemented per tool.
func mcpCodeSession(client API, box string) (*coderun.Session, func(), error) {
	target, privPath, cleanup, err := mcpCodeTarget(client, box)
	if err != nil {
		return nil, nil, err
	}
	// No remote command — Connect appends "agent-box" itself.
	sess, err := coderun.Connect(context.Background(), connectcore.BuildSSHArgs(target, privPath, ""))
	if err != nil {
		cleanup()
		// Same sentence the CLI gives (#2030): a box with no agent-box used to
		// surface as a raw `transport closed`, which named neither the missing
		// helper nor the command that installs it.
		if errors.Is(err, coderun.ErrAgentBoxMissing) {
			return nil, nil, coderun.AgentBoxMissingError(box)
		}
		return nil, nil, fmt.Errorf("connect to agent-box on %q: %w", box, err)
	}
	return sess, func() { _ = sess.Close(); cleanup() }, nil
}

// mcpCodeTarget resolves the SSH target and an identity for box, preferring a
// short-lived certificate and falling back to the managed key — the same
// preference order handleConnect uses, and for the same reason: a certificate
// leaves nothing installed on the box.
func mcpCodeTarget(client API, box string) (connectcore.Target, string, func(), error) {
	target, err := mcpWaitConnectable(client, box, "", "")
	if err != nil {
		return connectcore.Target{}, "", nil, err
	}

	cert, err := issueCertForBox(client, target.User)
	switch {
	case err == nil:
		return target, cert.PrivateKeyPath, cert.Cleanup, nil
	case errors.Is(err, errCertUnsupported):
		pubPath, pub, _, kerr := sshkey.LocateOrGenerate(sshkey.LocateOpts{})
		if kerr != nil {
			return connectcore.Target{}, "", nil, fmt.Errorf("locate or generate managed key: %w", kerr)
		}
		if aerr := mcpAuthorizeKey(client, box, pub); aerr != nil {
			return connectcore.Target{}, "", nil, fmt.Errorf("authorize key on %q: %w", box, aerr)
		}
		return target, strings.TrimSuffix(pubPath, ".pub"), func() {}, nil
	default:
		// The server COULD sign and something went wrong. Falling back would
		// install a long-lived key to paper over a control-plane fault and
		// never mention it — surface it instead.
		return connectcore.Target{}, "", nil, fmt.Errorf("issue certificate for %q: %w", box, err)
	}
}

func codeRunNameArg(args map[string]interface{}) string {
	if n := strings.TrimSpace(getStringArg(args, "name", "")); n != "" {
		return n
	}
	return coderun.DefaultRunName
}

// handleCodeRun starts a detached coding run and returns the first window of
// its output. The run outlives this call — that is the entire point — so the
// response carries what a caller needs to follow it: the run name and the
// offset to resume from.
func handleCodeRun(client API, args map[string]interface{}) (string, error) {
	box := strings.TrimSpace(getStringArg(args, "box", ""))
	if box == "" {
		return "", fmt.Errorf("`box` is required")
	}
	prompt := strings.TrimSpace(getStringArg(args, "prompt", ""))
	if prompt == "" {
		return "", fmt.Errorf("`prompt` is required")
	}
	streamJSON := getBoolArg(args, "stream_json", false)
	name := codeRunNameArg(args)
	sessionID := strings.TrimSpace(getStringArg(args, "session_id", ""))

	sess, done, err := mcpCodeSession(client, box)
	if err != nil {
		return "", err
	}
	defer done()

	command := coderun.BuildClaudeRunCommand(prompt, streamJSON)
	if sessionID != "" {
		// Mirrors claudeEngine.RunCommand's --resume handling (#2193). This
		// tool only ever builds a Claude command (it predates the engine
		// seam, #1727) — session resume is applied the same way here.
		command += " --resume " + coderun.ShellQuoteSingle(sessionID)
	}

	ctx := context.Background()
	// Snapshot the engine's session directory before the run starts, so
	// discovery adopts only a session this run creates (a resumed run's id is
	// the literal sessionID and needs none).
	discover := sessionID == "" && mcpSnapshotSessionDirectory(ctx, sess, name)
	started, err := sess.ProcessStart(ctx,
		name,
		command,
		"",
		coderun.CaptureModeFor(streamJSON),
	)
	if err != nil {
		return "", fmt.Errorf("start run on %q: %w", box, err)
	}
	if discover {
		mcpStartSessionDiscovery(ctx, sess, name)
	}

	out, next := readCodeWindow(ctx, sess, started.LogPath, 0, streamJSON)
	return fmt.Sprintf(
		"✓ started %q (pid %d) on %s\n%s\n\nlog_path: %s\nnext_offset: %d\n\n--- output so far ---\n%s\n"+
			"(the run continues on the box — call code_attach with name=%q and offset=%d for more)",
		started.Name, started.PID, box,
		// This tool only builds the Claude tenant-secret command, so its
		// traffic never crosses the gateway.
		engine.ScanningLine(engine.ModelTrafficScanning(engine.KindSecret, pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_UNSPECIFIED)),
		started.LogPath, next, out, started.Name, next), nil
}

// mcpSessionSidecar is where the run called name keeps its session sidecar:
// process_start puts every run's files under coderun.RunLogDir.
func mcpSessionSidecar(name string) string {
