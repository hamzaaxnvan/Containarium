package policyreadiness

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestServices_OnePerPolicyServicePlusCorePostgres(t *testing.T) {
	got := Services()
	want := []string{
		pb.GuardrailPolicyService_ServiceDesc.ServiceName,
		pb.CodingToolEgressPolicyService_ServiceDesc.ServiceName,
		CorePostgresService,
	}
	if len(got) != len(want) {
		t.Fatalf("Services() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Services()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRun_ReportsEachServiceAndClearsWhenServing(t *testing.T) {
	h := health.NewServer()
	for _, svc := range Services() {
		h.SetServingStatus(svc, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	results := Run(context.Background(), ServerCheck(h))
	if len(results) != len(Services()) {
		t.Fatalf("Run returned %d results, want %d", len(results), len(Services()))
	}
	for _, r := range results {
		if r.Ready() {
			t.Errorf("%s: Ready() = true while NOT_SERVING", r.Service)
		}
		if !strings.Contains(r.Detail(), "unavailable") {
			t.Errorf("%s: Detail() = %q, want it to say unavailable", r.Service, r.Detail())
		}
	}

	for _, svc := range Services() {
		h.SetServingStatus(svc, healthpb.HealthCheckResponse_SERVING)
	}
	for _, r := range Run(context.Background(), ServerCheck(h)) {
		if !r.Ready() {
			t.Errorf("%s: Ready() = false once SERVING (detail %q)", r.Service, r.Detail())
		}
	}
}

