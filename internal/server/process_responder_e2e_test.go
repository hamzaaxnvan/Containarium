//go:build incus

// Real-Incus coverage for the process responder's actuator path (#2636):
// THROTTLE applies the policy's allowance as a HARD CFS quota (the kernel's
// cpu.max is checked when readable, #2676: a bare "N%" is only a soft share
// and left the box unthrottled) and release restores the recorded limit; FREEZE leaves the instance Frozen and release
// resumes it. Needs a running container to act on:
//
//	CONTAINARIUM_E2E_INCUS_CONTAINER=<name> go test -tags=incus -run TestProcessResponderE2E ./internal/server/
//
// Skips (does not fail) without one. It lives under the `incus` tag (the
// repo's real-Incus lane), so the store-integration lane never compiles it.
// The sprint's batched verification pass on dev runs the full CPU-burner
// version from the issue's Verify-on-dev steps.
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestProcessResponderE2E_ThrottleAndFreezeOnRealIncus(t *testing.T) {
	name := os.Getenv("CONTAINARIUM_E2E_INCUS_CONTAINER")
	if name == "" {
		t.Skip("CONTAINARIUM_E2E_INCUS_CONTAINER is unset; needs a running Incus container to throttle and freeze")
	}
	client, err := incus.New()
	if err != nil {
		t.Skipf("no Incus here: %v", err)
	}
	info, err := client.GetContainer(name)
	if err != nil {
		t.Fatalf("GetContainer(%s): %v", name, err)
	}
	if info.State != "Running" {
		t.Fatalf("container %s is %s, want Running", name, info.State)
	}
	act := incusActuator{c: client}
	origCount, origAllow, err := act.CPULimit(name)
	if err != nil {
		t.Fatalf("CPULimit: %v", err)
	}
	t.Cleanup(func() {
		_ = act.Unfreeze(name)
		_ = act.SetCPULimit(name, origCount, origAllow)
	})

	ctx := context.Background()
	fx := &responderFixture{
		act:      nil,
		findings: newFakeFindings(),
		actions:  threatdetect.NewMemActionStore(),
		policies: threatdetect.NewProcessPolicyStore(nil),
		audit:    &fakeAudit{},
	}
	r := NewProcessResponder(fx.policies, fx.actions, fx.findings, act, fx.audit, false)
	sink := NewActingSink(fx.findings, r)
	fire := func(subject string) {
		t.Helper()
		if _, err := sink.Upsert(ctx, &threatdetect.Finding{
			Rule: pb.ThreatRuleId(6), Severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
			TenantID: "e2e", Container: name, Subject: subject,
		}); err != nil {
