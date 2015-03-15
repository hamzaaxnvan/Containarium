// Package runlog is the client side of TailRunLog (#2096): the one Go
// function `containarium crew logs` and the MCP `crew_logs` tool both call.
package runlog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// FollowSeconds is how long each --follow call lets the daemon wait for new
// bytes (the RPC's upper bound).
const FollowSeconds = 10

// Tailer is the one RPC this package needs; the gRPC, HTTP and MCP clients
// all satisfy it.
