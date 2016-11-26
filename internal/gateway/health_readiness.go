package gateway

import (
	"encoding/json"
	"net/http"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/pkg/version"
)

// SetReadinessCheck supplies the daemon's in-process readiness source before
// Start. It must not perform network I/O: /health is a liveness endpoint.
func (gs *GatewayServer) SetReadinessCheck(check policyreadiness.CheckFunc) {
	gs.readinessCheck = check
}

func healthHandlerWithReadiness(check policyreadiness.CheckFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
