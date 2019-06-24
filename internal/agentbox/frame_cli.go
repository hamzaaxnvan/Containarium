//go:build !windows

package agentbox

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/footprintai/containarium/internal/logframe"
)

// `agent-box frame <stream> <log>` — the child-side half of framed capture
// (#1701).
//
// Framing used to happen inside agent-box: cmd.Stdout was a frameWriter, an
// io.Writer rather than an *os.File, so os/exec put a pipe between the child
// and a copier goroutine in agent-box's address space. agent-box is a stdio
// MCP server spawned per SSH connection, so when the connection dropped its
// exit closed the read end and the child died of SIGPIPE on its next write —
// destroying exactly the detached run that #1672/#1674 exist to keep alive.
// Combined mode never had the bug: cmd.Stdout was the *os.File itself, whose
// descriptor the child inherits.
//
// So framing moves to the child's side of the fork. spawnBackgroundProcess
// starts one of these per stream, inside the same setsid'd shell as the
// command, reading a FIFO the command writes to. Nothing in the path depends
// on agent-box still being alive.
//
// Both framers append to ONE log, so the single-offset contract tail_log
// serves is unchanged. They serialize with an flock on the log file: frames
// can exceed PIPE_BUF, so O_APPEND alone would not keep two writers from
// interleaving mid-frame.
func RunFrameCLI(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: agent-box frame <1|2> <log-path>")
		return 2
	}
	// logframe.Stream is the ASCII BYTE the wire format uses ('1'/'2'), not
	// the integer 1/2 — so match the argument against the constants directly
	// rather than parsing it as a number and comparing domains.
	var stream logframe.Stream
	switch args[0] {
	case string(rune(logframe.Stdout)):
		stream = logframe.Stdout
	case string(rune(logframe.Stderr)):
		stream = logframe.Stderr
