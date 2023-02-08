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

func TestMinerSignatureE2E_FindingKillAndAudit(t *testing.T) {
	name := os.Getenv("CONTAINARIUM_E2E_INCUS_CONTAINER")
	if name == "" {
		t.Skip("CONTAINARIUM_E2E_INCUS_CONTAINER is unset; needs a running Incus box to exec in")
	}
	obj := os.Getenv("CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT")
	if obj == "" {
		t.Skip("CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT is unset; needs the execwatch object")
	}
	client, err := incus.New()
	if err != nil {
		t.Skipf("no Incus here: %v", err)
	}
	initPID, err := client.InitPID(name)
	if err != nil || initPID <= 0 {
		t.Fatalf("InitPID(%s) = %d, %v; want a running box", name, initPID, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Responder with the real host killer; kill_process on, policy ALERT so
	// the box itself is left alone.
	findings := newFakeFindings()
	policies := threatdetect.NewProcessPolicyStore(nil)
	if err := policies.SetDefault(ctx, &pb.ProcessPolicy{Action: pb.ProcessAction_PROCESS_ACTION_ALERT, KillProcess: proto.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	auditLog := &fakeAudit{}
	responder := NewProcessResponder(policies, threatdetect.NewMemActionStore(), findings, incusActuator{c: client}, auditLog, false)
	responder.SetProcessKiller(newHostProcessKiller(client.InitPID))
	go responder.Run(ctx) // the async sink's worker, as in production

	engine := threatdetect.NewEngine(NewAsyncActingSink(findings, responder), "e2e", false, nil, nil)
	rule, err := threatdetect.NewMinerSignatureRule(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.Register(rule)

	// Every file goes in through the Incus file API, and each stub is
	// started by a launcher script run with no arguments, so no exec but the
	// stub itself ever carries a marker in its argv: a wrapper `sh -c "...
	// --donate-level 1"` would match (and be killed) first.
	write := func(path, content string) {
		t.Helper()
		if err := client.WriteFile(name, path, []byte(content), "0755"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	launcher := func(cmdline string) string {
		// HUP ignored so the stub outlives the exec session; the
		// disposition survives the stub's exec.
		return "#!/bin/sh\ntrap '' HUP\n" + cmdline + " >/dev/null 2>&1 &\n"
	}
	write("/tmp/xmrig", minerStub)
	write("/tmp/gcc", minerStub)
	write("/tmp/worker", minerStub)
	write("/tmp/launch-a", launcher("/tmp/xmrig --donate-level 1"))
	write("/tmp/launch-b", launcher("/tmp/gcc -O2"))
	write("/tmp/launch-c", launcher("/tmp/worker -o stratum+tcp://pool.invalid:3333"))
	t.Cleanup(func() {
		_, _, _ = client.ExecWithOutput(name, []string{"sh", "-c", "pkill -f /tmp/xmrig; pkill -f /tmp/gcc; pkill -f /tmp/worker; rm -f /tmp/xmrig /tmp/gcc /tmp/worker /tmp/launch-a /tmp/launch-b /tmp/launch-c"})
	})
	run := func(path string) {
		t.Helper()
		if _, stderr, err := client.ExecWithOutput(name, []string{path}); err != nil {
			t.Fatalf("exec %s: %v (%s)", path, err, stderr)
		}
	}

	watcher := NewExecWatcher(engine.OnExec)
	if err := watcher.Start(ctx, obj); err != nil {
		t.Skipf("exec telemetry unavailable here: %v", err)
	}
	defer watcher.Stop()
	watcher.Reconcile([]incus.ContainerInfo{{Name: name, State: "Running", Pid: initPID, Tenant: "e2e"}})

