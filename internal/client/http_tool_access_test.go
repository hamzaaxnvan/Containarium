package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The tool-access level is sent as the enum's proto JSON name (the gateway
// decodes it into BoxToolAccess), to the documented route, and the response
// decodes back into the enum.
func TestHTTPSetContainerToolAccess_WireFormat(t *testing.T) {
	rec, c := newBodyRecorder(t, `{"toolAccess":"BOX_TOOL_ACCESS_FULL_SUDO"}`)
	resp, err := c.SetContainerToolAccess("alice", pb.BoxToolAccess_BOX_TOOL_ACCESS_FULL_SUDO)
	if err != nil {
		t.Fatalf("SetContainerToolAccess: %v", err)
