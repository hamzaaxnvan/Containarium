package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeSessionsAPI struct {
	calls  []string
	closed bool
}

func (f *fakeSessionsAPI) TerminateUserSessions(username string) (*pb.TerminateUserSessionsResponse, error) {
	f.calls = append(f.calls, username)
	return &pb.TerminateUserSessionsResponse{TerminatedSessions: 4, BoxesTouched: 3}, nil
}

func (f *fakeSessionsAPI) Close() error { f.closed = true; return nil }

// The terminate_user_sessions tool is a thin wrapper over the CLI's shared
// client call (client.SessionsAPI.TerminateUserSessions), not an HTTP path of
// its own (#2522): with the shared client faked, the tool reaches the fake
// once and the MCP transport sends nothing.
