package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/guardrail"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// piiOnlyEngine is a GuardrailEngineService that, like a real detector
// without a secrets layer, refuses a request naming SECRET with
// FAILED_PRECONDITION and otherwise defers to the reference rules engine.
// Served over real gRPC on loopback so the CLI's --engine path is the one
// under test.
