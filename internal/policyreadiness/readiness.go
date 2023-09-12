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
