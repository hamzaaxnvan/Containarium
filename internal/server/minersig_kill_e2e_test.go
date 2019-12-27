//go:build incus

// Real-kernel, real-Incus coverage for the miner-signature rule and the
// kill_process path (#2638): a stub named `xmrig` run with
// `--donate-level 1` inside a box raises a HIGH MINER_SIGNATURE finding with
// the exec attached, is SIGKILLed from the host (kill_process=true) and the
// kill is audited with its pid; the same stub named `gcc` raises nothing
// from this rule. Needs root on an Incus host with kernel BTF, the execwatch
// object, and a running box to exec in:
//
//	sudo CONTAINARIUM_E2E_INCUS_CONTAINER=<name> \
//	  CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT=<path to netpolicy.bpf.o | embedded> \
//	  go test -tags=incus -run TestMinerSignatureE2E ./internal/server/
//
// Skips (does not fail) without them. CI has no such host; the sprint's
// verification pass runs it (the issue's Verify-on-dev steps).
package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// minerStub busy-loops, so the `gcc` copy has the same CPU profile as the
// `xmrig` one; only the name and argv differ.
const minerStub = "#!/bin/sh\nwhile :; do :; done\n"

