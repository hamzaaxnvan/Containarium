package security

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Rogue SSH listener detection (#2439).
//
// The sshd posture pass (#2424) keeps the distro sshd key-only, but the box
// owner is root and can start a second SSH server: a hand-built `sshd -f`,
// dropbear, or the stock binary on another port, typically bound to
// loopback and exposed through a reverse tunnel. This check finds those: a
// listening TCP socket held by an SSH-server process that is not the distro
// sshd on a port its sshd_config declares.
//
// Scope, stated plainly: this finds the casual and the accidental. A root
// tenant who renames the binary, or embeds an SSH server in another program,
// is invisible here; stopping that is an egress problem (NetworkPolicy
// ENFORCE). Record-only: a process is the tenant's, so unlike the managed
// drop-in it is never killed or modified.

// listenerProbeTimeout bounds one box's probe so a wedged container cannot
// stall the whole pass.
const listenerProbeTimeout = 30 * time.Second

// maxProbeOutput caps how much of a probe's output is parsed. A box with a
// huge number of descriptors must not turn a 10-minute housekeeping pass
// into a memory problem.
const maxProbeOutput = 4 << 20

// listenerProbeScript prints the box's listening-socket tables, which
// process holds which socket, and each process's executable. It forks a
// handful of times in total (cat, cat, ls, ls) regardless of how many
// processes the box runs, and reads nothing tenant-supplied except what
// those files contain. Output sections are introduced by @-lines.
const listenerProbeScript = `echo @tcp; cat /proc/net/tcp 2>/dev/null
echo @tcp6; cat /proc/net/tcp6 2>/dev/null
echo @fd; ls -l /proc/[0-9]*/fd 2>/dev/null
echo @exe; ls -l /proc/[0-9]*/exe 2>/dev/null
true`

// sshServerBinaries are executable base names that are SSH servers.
var sshServerBinaries = map[string]bool{
	"sshd":          true,
	"dropbear":      true,
	"dropbearmulti": true,
	"tinysshd":      true,
}

// stockSSHDDirs are where the distro's sshd binary lives (merged-/usr and
// split layouts).
var stockSSHDDirs = map[string]bool{"/usr/sbin": true, "/sbin": true}

// listenSocket is one LISTEN row of /proc/net/tcp or tcp6.
type listenSocket struct {
	Port  int
	Bind  string
	Inode string
}

// procSnapshot is one probe's parsed output.
type procSnapshot struct {
	Sockets []listenSocket
	// Owners maps a socket inode to every pid holding a descriptor on it.
	Owners map[string][]int
	// Exes maps a pid to its executable path.
	Exes map[int]string
}

var (
	reSocketFD = regexp.MustCompile(`socket:\[(\d+)\]`)
	reFDHeader = regexp.MustCompile(`^/proc/(\d+)/fd:$`)
	reExeLink  = regexp.MustCompile(`/proc/(\d+)/exe -> (.+)$`)
)

// parseSnapshot parses listenerProbeScript's output. Malformed lines are
// skipped: the input is partly tenant-influenced (paths, names) and a
// parse problem must degrade to "fewer findings", never to a panic.
func parseSnapshot(out string) procSnapshot {
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput]
	}
	snap := procSnapshot{Owners: map[string][]int{}, Exes: map[int]string{}}
	section := ""
	curPID := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch line {
		case "@tcp", "@tcp6", "@fd", "@exe":
			section = line
			curPID = 0
			continue
		}
		switch section {
		case "@tcp":
			if s, ok := parseProcNetTCPLine(line, false); ok {
				snap.Sockets = append(snap.Sockets, s)
			}
		case "@tcp6":
			if s, ok := parseProcNetTCPLine(line, true); ok {
				snap.Sockets = append(snap.Sockets, s)
			}
		case "@fd":
			if m := reFDHeader.FindStringSubmatch(line); m != nil {
