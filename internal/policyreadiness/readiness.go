// Package policyreadiness reports whether the daemon's policy stores are
// ready (#2483). A store is ready once its durable (Postgres) store is
// installed; until then its RPCs fail closed. Readiness is the standard
// grpc.health.v1 service, one status per policy service. The daemon's status
// probe reads it in-process; `containarium doctor` reads it through the REST
// gateway.
package policyreadiness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// HTTPPath is where the REST gateway serves grpc.health.v1.Health/Check, with
// the service name in the `service` query parameter.
const HTTPPath = "/v1/health"

// CorePostgresService is the readiness entry for the daemon's startup
// Postgres check (#2824). NOT_SERVING means the check failed within its
// deadline, so the daemon is serving DEGRADED: every database-backed store
// was skipped at startup and stays off until the daemon restarts. A daemon
// with no Postgres configured does not report it.
const CorePostgresService = "containarium.core_postgres"

// Services returns the health service name of each policy store, in report
// order.
func Services() []string {
	return []string{
		pb.GuardrailPolicyService_ServiceDesc.ServiceName,
		pb.CodingToolEgressPolicyService_ServiceDesc.ServiceName,
		CorePostgresService,
	}
}

// displayNames are the operator-facing names doctor prints.
var displayNames = map[string]string{
	pb.GuardrailPolicyService_ServiceDesc.ServiceName:        "guardrail policy store",
	pb.CodingToolEgressPolicyService_ServiceDesc.ServiceName: "coding-tool egress policy store",
	CorePostgresService: "core Postgres (database-backed stores)",
}

// CheckFunc asks for one service's serving status. An error means the
// status could not be read at all (for example, the daemon is not running).
type CheckFunc func(ctx context.Context, service string) (healthpb.HealthCheckResponse_ServingStatus, error)

// Result is one policy store's readiness.
type Result struct {
	Service string
	Status  healthpb.HealthCheckResponse_ServingStatus
	// Err is set when the status could not be read.
	Err error
}

// Name is the store's operator-facing name.
func (r Result) Name() string {
	if n, ok := displayNames[r.Service]; ok {
		return n
	}
	return r.Service
}

// Ready reports whether the store is serving from its durable store.
func (r Result) Ready() bool {
	return r.Err == nil && r.Status == healthpb.HealthCheckResponse_SERVING
}

// Detail explains the result in one line.
