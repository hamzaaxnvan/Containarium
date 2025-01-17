package cmd

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
)

// Exercise the HTTP client through the CLI: decoding memory without carrying
// it into the rendered output was the remaining gap after #3252 (#3292).
func TestInfoHTTPMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name string
		info string
