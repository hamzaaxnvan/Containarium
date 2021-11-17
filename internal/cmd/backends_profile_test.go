package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestBackendsProfileDaemonJSON exercises the actual protobuf JSON wire shape:
// operators must be able to read capacities and benchmark scores in text mode.
func TestBackendsProfileDaemonJSON(t *testing.T) {
	profile := &pb.CapabilityProfile{
		CpuCores: 8, TotalMemoryBytes: 16 << 30, TotalDiskBytes: 128 << 30,
		MeasuredClass: "cpu-small", ClassConsistent: true,
		Benchmark: &pb.CapabilityBenchmark{CpuOpsPerSec: 123456, MemBytesPerSec: 2 << 30, DurationMs: 300},
	}
	for _, get := range []bool{false, true} {
		method := http.MethodPost
		if get {
			method = http.MethodGet
		}
		for _, mode := range []string{"text", "json", "unprofiled"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				p := profile
				if mode == "unprofiled" {
					p = nil
				}
				var response proto.Message = &pb.ProfileBackendResponse{BackendId: "peer-test", Profile: p}
				if get {
					response = &pb.GetCapabilityProfileResponse{BackendId: "peer-test", Profile: p}
				}
				body, err := protojson.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != method || r.URL.Path != "/v1/capabilities/profile" {
						t.Errorf("request = %s %s, want %s /v1/capabilities/profile", r.Method, r.URL.Path, method)
					}
					if get && r.URL.Query().Get("backend_id") != "peer-test" {
						t.Error("GET must select the requested backend")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}))
