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
