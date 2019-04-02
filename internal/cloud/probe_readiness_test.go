//go:build !windows

package cloud

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/policyreadiness"
)

// #2483: the status probe carries policy-store readiness as advisory
// checks: reported unmet while a store is unavailable, met once it is
// installed, and never folded into SelfCheckOK.
func TestProbe_ReportsPolicyStoreReadiness(t *testing.T) {
	h := health.NewServer()
	for _, svc := range policyreadiness.Services() {
		h.SetServingStatus(svc, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	probe := DefaultStatusProbe{Readiness: policyreadiness.ServerCheck(h)}
	baseline, err := DefaultStatusProbe{}.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	readiness := func(st HostStatus) map[string]HostCheck {
		out := map[string]HostCheck{}
		for _, c := range st.Checks {
			if strings.HasPrefix(c.Name, "readiness: ") {
				out[c.Name] = c
			}
		}
		return out
	}

	st, err := probe.Probe(context.Background())
	if err != nil {
