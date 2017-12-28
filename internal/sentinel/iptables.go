package sentinel

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const chainName = "SENTINEL_PREROUTING"

// sshPiperPort is the port used by sshpiper on the sentinel.
// Port 22 traffic is handled by sshpiper directly, not forwarded via DNAT.
const sshPiperPort = 22

// enableForwarding sets up iptables DNAT rules to forward traffic to the spot VM.
// It creates a custom chain, adds DNAT rules for each port, and enables MASQUERADE.
// Port 22 is excluded from forwarding — it is handled by sshpiper on the sentinel.
// On non-Linux systems or when iptables is unavailable, it logs a warning and returns nil.
// hostGOOS is runtime.GOOS, indirected so the non-Linux early returns below
// are testable on any platform.
//
// Without it, the tests that cover those returns run the real iptables path
// when the test host is Linux — which is every CI runner. They passed by
// actually writing DNAT rules into the runner's nat table, and failed on any
// Linux machine without the privilege to do so. Neither outcome had anything
// to do with the behaviour they claim to check.
var hostGOOS atomic.Value // string

func init() { hostGOOS.Store(runtime.GOOS) }

// goos reports the OS the helpers below should behave as.
func goos() string { return hostGOOS.Load().(string) }

func enableForwarding(spotIP string, ports []int) error {
