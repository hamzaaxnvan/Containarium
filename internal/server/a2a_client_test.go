package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestSendA2ATask exercises the A2A client against a stub peer that speaks the
// AgentTask -> AgentArtifact contract (what the in-box A2A server will do).
