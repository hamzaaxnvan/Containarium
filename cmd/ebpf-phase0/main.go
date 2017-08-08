// Command ebpf-phase0 is the Go-side validator for the eBPF
// network isolation design's Phase 0.
//
// The shell-only validator (experimental/ebpf-phase0/validate.sh)
// confirms the kernel + Incus + tc-bpf path works. This binary
// confirms the production Go library — github.com/cilium/ebpf —
// can drive the same path end-to-end.
//
// If both Phase 0 validators pass on a Containarium backend,
// Phase A productionalizes. If this binary fails but the shell
// path passes, cilium/ebpf has an unexpected rough edge on the
// kernel + interface combination we target — investigate before
// committing to it in Phase A.
//
// THROWAWAY. Not for production. Run with --help for usage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

func main() {
	var (
		obj        = flag.String("obj", "experimental/ebpf-phase0/counter.bpf.o", "Path to the compiled counter.bpf.o")
		bridge     = flag.String("bridge", "incusbr0", "Bridge interface to attach to")
		watchEvery = flag.Duration("watch-every", 2*time.Second, "Counter print interval; 0 = once and exit")
	)
	flag.Parse()

	if err := run(*obj, *bridge, *watchEvery); err != nil {
		log.Fatalf("phase 0 go validator: %v", err)
	}
}

func run(objPath, bridge string, every time.Duration) error {
	// Bump memlock — older kernels need this for any BPF map
	// allocation. Kernel 5.11+ unified BPF memory into cgroup
	// accounting and the call is a no-op there, but it's free
	// insurance.
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("RemoveMemlock: %w", err)
	}

	// Look up the bridge so we fail fast on a typo'd interface.
	iface, err := net.InterfaceByName(bridge)
	if err != nil {
		return fmt.Errorf("interface %q: %w", bridge, err)
	}

	// Resolve the BPF object path. Allow caller to pass a relative
	// path against the repo root for ergonomics.
	absObj, err := filepath.Abs(objPath)
	if err != nil {
