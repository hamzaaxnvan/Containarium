package server

import (
	"context"
	"encoding/json"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/metadata"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

