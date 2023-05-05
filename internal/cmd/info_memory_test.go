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
		want string
	}{
		{
			name: "gateway quoted int64s",
			info: `{"incusVersion":"6.0.2","kernelVersion":"6.8.0","os":"Ubuntu 24.04",
				"totalMemoryBytes":"34359738368","availableMemoryBytes":"17179869184"}`,
			want: "Memory:\n  Total:           32.0 GiB\n  Available:       16.0 GiB\n",
		},
		{
			name: "zero available is reported",
			info: `{"totalMemoryBytes":"34359738368","availableMemoryBytes":"0"}`,
			want: "Memory:\n  Total:           32.0 GiB\n  Available:       0.0 GiB\n",
		},
		{
			name: "older daemon without memory",
			info: `{"incusVersion":"6.0.2"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
