package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/metrics/platformstats"
	"github.com/footprintai/containarium/pkg/core/box"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
)

// No real instance or host user is created: the server reaches a backend that
// captures the spec and returns a simulated Guard failure. Positive lifecycle
// and actual ACL attachment are tested separately against fake Incus backends.
type nativeTransportBackend struct {
	incus.Backend
	info incus.ContainerInfo
}

func (b *nativeTransportBackend) ListContainers() ([]incus.ContainerInfo, error) {
	return []incus.ContainerInfo{b.info}, nil
}
func (b *nativeTransportBackend) GetContainer(string) (*incus.ContainerInfo, error) {
	out := b.info
	return &out, nil
}
