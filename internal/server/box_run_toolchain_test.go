package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A configured level is not enough: BUILD_TOOLS must have the managed mount.
// A missing host installation must not prevent NONE or FULL_SUDO runs.
