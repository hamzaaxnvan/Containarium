//go:build !windows

package agentbox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// `agent-box approval-mcp` (#2183): the permission-prompt tool behind
// `code run --approvals ask`.
//
// Claude Code starts it as a stdio MCP server (--mcp-config) and calls its one
// tool for every tool call that needs a permission (--permission-prompt-tool).
// The call blocks until a signed answer arrives or the request expires into a
// deny, and every step is journaled to <name>.input.jsonl.
//
// Why answers are signed: the agent runs as the same Unix user as anyone
// answering on the box, so any file an answerer can write, the agent can write
// too. An answer counts only with an Ed25519 signature that verifies under the
// box's approval public key, which arrives on this process's command line from
// `code run`. The private key stays with the daemon, so there is no secret on it for
// any process to read. The signature also binds the tool call this process
// journaled, its complete input (#2184): the agent can rewrite that journal
// line to show a human something else, but an answer made for the shown text
// never verifies against the tool call held here, and an allow returns the
// exact input that was signed for. Out of scope: a process the agent leaves running can
// change the box's own Claude Code setup (its binaries on PATH, its settings)
// for later runs; no scheme confined to the box can prevent that.

// ApprovalToolName is the tool Claude Code's --permission-prompt-tool names,
// as mcp__<server>__<tool>.
const ApprovalToolName = "approve"

// ApprovalServerName is the MCP server key the run's --mcp-config uses.
const ApprovalServerName = "containarium-approval"

// approvalPollInterval is how often a waiting request looks for its answer.
const approvalPollInterval = 500 * time.Millisecond

// approvalProgressInterval keeps Claude Code's stdio idle timer (30 min by
// default) from firing while a request waits.
const approvalProgressInterval = 30 * time.Second

// approvalProbeOK is what `approval-mcp --probe` prints, so `code run` can
// tell an agent-box that has this mode from an older one before it starts a
// run that would otherwise fail to connect its permission tool. "v2" is the
// prompt-bound answer format (#2184): an agent-box that prints only
// "approval-mcp ok" verifies the older format and is refused.
const approvalProbeOK = "approval-mcp ok v2"

// approvalPromptMax caps the human-readable prompt journaled per request.
const approvalPromptMax = 500

// approvalServer is one run's approval channel.
type approvalServer struct {
	run     string
	dir     string
