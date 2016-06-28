package cloud

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
)

func TestReportStatusOnce_Posture(t *testing.T) {
	measuredAt := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	for _, tc := range []struct {
		name    string
		posture *HostSecurityPosture
	}{
		{"passing", &HostSecurityPosture{MeasuredAt: measuredAt, Checks: []HostCheck{{Name: "auditd running", OK: true, Detail: "running"}}}},
		{"failing", &HostSecurityPosture{MeasuredAt: measuredAt, Checks: []HostCheck{{Name: "auditd running", OK: false, Detail: "could not read"}}}},
		{"missing", nil},
		{"timestamp missing", &HostSecurityPosture{Checks: []HostCheck{{Name: "auditd running", OK: false}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, fake := newTestClient(t, &Config{ControlPlane: "bufnet", HostID: "h1", Token: "h1.secret", Insecure: true})
			st := HostStatus{SelfCheckOK: true, Posture: tc.posture, Checks: []HostCheck{{Name: "useradd", OK: true}}}
			// Two actual gRPC reports ensure the second snapshot does not
			// reuse the first report's result or timestamp.
			for i := range 2 {
				c.status = fakeProbe{st: st}
				c.reportStatusOnce()
				fake.mu.Lock()
				got := proto.Clone(fake.statusReq).(*cloudv1.ReportHostStatusRequest)
				reports, bearer := fake.statusReports, fake.statusBearer
				fake.mu.Unlock()
				if reports != i+1 || bearer != "h1.secret" {
					t.Fatalf("reports = %d, bearer = %q", reports, bearer)
				}
				if !got.GetSelfCheckOk() || len(got.GetChecks()) != 1 || got.GetChecks()[0].GetName() != "useradd" {
					t.Fatalf("legacy status changed: %v", got)
				}
				var want *cloudv1.HostSecurityPosture
				if st.Posture != nil {
					want = &cloudv1.HostSecurityPosture{}
					for _, ck := range st.Posture.Checks {
						want.Checks = append(want.Checks, &cloudv1.HostCapabilityCheck{Name: ck.Name, Ok: ck.OK, Detail: ck.Detail})
					}
					if !st.Posture.MeasuredAt.IsZero() {
						want.MeasuredAt = timestamppb.New(st.Posture.MeasuredAt)
					}
				}
				if !proto.Equal(got.GetPosture(), want) {
					t.Errorf("posture = %v, want %v", got.GetPosture(), want)
				}
				st.Posture = &HostSecurityPosture{MeasuredAt: measuredAt.Add(time.Minute), Checks: []HostCheck{{Name: "Secure Boot", OK: false, Detail: "disabled"}}}
			}
		})
	}
}
