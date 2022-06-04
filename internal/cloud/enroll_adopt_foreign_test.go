package cloud

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"

	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
)

// `cloud enroll --adopt-foreign` (cloud #1006).
//
// The cloud refuses to enroll a host that already runs other organizations'
// cloud-managed containers — the root cause of the cloud#1001 co-residency.
// This flag is the operator's explicit acknowledgement. The decision is made
// and audited control-plane-side; the host's only job is to transmit the flag
// faithfully on BOTH transports, so these tests pin exactly that.

func TestEnroll_CarriesAdoptForeign(t *testing.T) {
	tests := []struct {
		name string
		opts EnrollOptions
		want bool
	}{
		{
			name: "default is false — the guard stays armed unless asked",
			opts: EnrollOptions{DriverToken: "admin.jwt", OSSBackendID: "tunnel-1"},
			want: false,
		},
