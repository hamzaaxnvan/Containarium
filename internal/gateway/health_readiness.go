package gateway

import (
	"encoding/json"
	"net/http"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/pkg/version"
