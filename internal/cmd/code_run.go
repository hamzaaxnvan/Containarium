package cmd

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/spf13/cobra"
)

var (
	codeRunPrompt        string
	codeRunStreamJSON    bool
	codeAttachStreamJSON bool
	codeStopForce        bool

	// #1727: --continue maps to the engine's session-resume flag, and
	// --token-ttl bounds the gateway token minted for this one run.
	codeRunContinue bool
	codeRunTokenTTL string

	// #2193: --session resumes one SPECIFIC session id instead of --continue's
	// "most recent" — mutually exclusive with it (validated in runCodeRun).
	codeRunSession string
)

var codeRunCmd = &cobra.Command{
	Use:   "run <box>",
	Short: "Start a coding agent on a box and stream its output as it's produced",
	Long: `Starts the box's coding agent with --prompt, detached (it survives this
command's own connection dying — see Killing the local client does not kill
the run, below), and streams its output to your terminal as it's produced: not
buffered to completion, and with no timeout on the run itself.

Which agent, and where its model credential comes from, is read from
~/.containarium/code.json — written by 'containarium code install'. A box with
no such record runs Claude Code on the box's own environment, which is what
every box installed before that record existed already did.

On a box installed with --credential gateway, each run mints its OWN scoped,
short-lived gateway token (bound to this box, this run, and the recorded model)
and writes it to the engine's gateway.env at 0600 before the run starts. No
provider key is ever stored on the box. --token-ttl bounds that token; the
default is 24h and the server caps it.

--continue resumes the agent's most recent session for the run's working
directory instead of starting a fresh one.

A mid-run network drop is recovered automatically by resuming at the last
byte offset seen — nothing is re-issued, nothing is lost or duplicated.
Killing this command (Ctrl-C, closing the laptop) does NOT kill the run;
reconnect with 'containarium code attach <box>'.

This command's own process exit status reflects the AGENT's exit code once
the run finishes: zero when the agent exited zero, non-zero when it didn't —
so CI and shell scripts can gate on '$?' directly, without separately parsing
'containarium code status'. A run that finishes with no exit code recorded
(the box died mid-run) is not treated as a failure here; check 'code status'
for "unknown" in that case.

--approvals ask (Claude Code only) pauses the run when the agent wants a tool
call that needs a permission, instead of the engine refusing it: the request
is journaled next to the run's log, 'containarium code status' reports
waiting_input with the request, and the call proceeds on an allow or is
refused on a deny. Answer it inline (a or d + Enter while this command runs at
a terminal, or in 'code attach'), or with 'containarium code pending' and
'containarium code answer'. A request nobody answers expires into a deny after
--approval-timeout (default 30m), and the expiry is journaled. Answers are
signed by the daemon with a key per box; only the public key goes to the box,
so nothing on the box can forge an answer. An answer counts only for the
prompt it was signed for, which is the prompt the answering command printed:
a prompt the agent rewrites in the journal is never what gets allowed. (A
process an agent leaves running on the box can still change the box's own
Claude Code setup for later runs; that is out of scope.) --approvals ask needs
the daemon (--server). On an engine without a permission hook (pi, Codex) the
run starts unchanged, with a warning. The default, --approvals deny, is today's behaviour.

Requires the box to already have a coding agent installed and credentialed —
see 'containarium code install <box>'.`,
	Args: cobra.ExactArgs(1),
	RunE: runCodeRun,
}

var codeAttachCmd = &cobra.Command{
	Use:   "attach <box>",
	Short: "Reconnect to a running (or just-finished) run and replay its output",
	Long: `Reconnects to the run 'containarium code run' started (or one dispatched
another way with the same --name) and replays everything it has produced
since it started, byte-exact, then keeps streaming until it exits or you
disconnect again.
