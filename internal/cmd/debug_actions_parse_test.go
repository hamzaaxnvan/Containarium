//go:build !windows && !containarium_client

package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/server"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Every containarium command `containarium debug` prints must actually parse
// (#1478).
//
// The bug: the missing-host-user remediation recommended
// `sync-accounts --user <name>` while sync-accounts registered only --dry-run
// and --force, so the printed command died with "unknown flag: --user". This
// text is read by someone already locked out, following it literally, and its
// failure invites the reading that the DIAGNOSIS was wrong when only the remedy
// was mistyped.
//
// Nothing structural stops that drift — the strings are fmt.Sprintf literals in
// a different package from the flag definitions — so it has to be a test.

// diagnoseStates covers every branch of Diagnose that can emit a command.
func diagnoseStates() []*pb.DebugContainerResponse {
	return []*pb.DebugContainerResponse{
		{ContainerState: "missing"},
		{ContainerState: "stopped"},
		{ContainerState: "running", HostUserExists: false},
		{ContainerState: "running", HostUserExists: true, HostUserShell: "/usr/local/bin/containarium-shell"},
		{ContainerState: "running", HostUserExists: true, HostUserShell: "/nonexistent", HostUserShellExists: false},
		{ContainerState: "error: incus unreachable"},
		{ContainerState: "running", HostUserExists: true, SshIngressHost: "sentinel.example.com"},
	}
}

// extractContainariumCommands pulls each `containarium ...` invocation out of a
// next_actions line. A line is prose with the command embedded, and may chain
// two commands with &&.
//
// "containarium" also appears as an ARGUMENT to other tools in these hints —
// `journalctl -u containarium -e --no-pager` — so a bare substring match
// mistakes a unit name for a command and then reports a bogus failure. Require
// the following token to look like a subcommand (not a flag) to tell them
// apart.
func extractContainariumCommands(action string) []string {
	var out []string
	for _, chunk := range strings.Split(action, "&&") {
		idx := strings.Index(chunk, "containarium ")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(chunk[idx+len("containarium "):])
		verb, _, _ := strings.Cut(rest, " ")
		if verb == "" || strings.HasPrefix(verb, "-") {
			continue // e.g. `journalctl -u containarium -e` — not an invocation
		}
		out = append(out, "containarium "+rest)
	}
	return out
}

// tokenize splits a command into argv, dropping the leading binary name and
// stopping at placeholders like <pubkey> which are meant to be substituted.
func tokenize(cmdline string) []string {
	fields := strings.Fields(cmdline)
	if len(fields) > 0 && fields[0] == "containarium" {
		fields = fields[1:]
	}
	var argv []string
	for _, f := range fields {
		if strings.HasPrefix(f, "<") {
			// Placeholder value for the preceding flag; supply something that
			// parses so the flag itself is still validated.
			argv = append(argv, "placeholder")
			continue
		}
		argv = append(argv, f)
	}
	return argv
}

func TestDebugRemediationCommandsParse(t *testing.T) {
	for _, report := range diagnoseStates() {
		_, actions := server.Diagnose("alice", report)
		for _, action := range actions {
			for _, cmdline := range extractContainariumCommands(action) {
				t.Run(cmdline, func(t *testing.T) {
					argv := tokenize(cmdline)
					if len(argv) == 0 {
						t.Fatalf("no argv extracted from %q", cmdline)
					}

					// Resolve against the REAL command tree, then parse the
