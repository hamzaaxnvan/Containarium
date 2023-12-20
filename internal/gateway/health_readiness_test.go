package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/pkg/version"
)

func TestHealthEndpoint_CorePostgresReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status healthpb.HealthCheckResponse_ServingStatus
		err    error
		want   healthStatus
