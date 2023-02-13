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

// Build the pre-#2328 schema by removing only field 13 from the current
// descriptor. This exercises the real protobuf/gateway decoders without
// registering a conflicting generated copy of the old Go types.
func legacyStatusDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	fd := protodesc.ToFileDescriptorProto(cloudv1.File_containarium_cloud_v1_actuation_service_proto)
	for _, m := range fd.MessageType {
		if m.GetName() != "ReportHostStatusRequest" {
			continue
		}
		fields := m.Field[:0]
		for _, f := range m.Field {
			if f.GetNumber() != 13 {
				fields = append(fields, f)
			}
		}
		m.Field = fields
	}
	f, err := protodesc.NewFile(fd, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	md := f.Messages().ByName("ReportHostStatusRequest")
	if md.Fields().Len() != 12 || md.Fields().ByName("posture") != nil {
		t.Fatal("legacy schema must contain only the original 12 fields")
	}
	return md
}

func TestReportHostStatus_BackwardCompatibility(t *testing.T) {
	md := legacyStatusDescriptor(t)
	req := &cloudv1.ReportHostStatusRequest{
		AgentVersion: "test", CpuCores: 8, SelfCheckOk: true,
		Checks: []*cloudv1.HostCapabilityCheck{{Name: "posture: auditd running", Ok: false, Detail: "not running"}},
		Posture: &cloudv1.HostSecurityPosture{
			Checks:     []*cloudv1.HostCapabilityCheck{{Name: "auditd running", Ok: false, Detail: "not running"}},
			MeasuredAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)),
		},
	}
	assertLegacy := func(t *testing.T, got *dynamicpb.Message) {
		t.Helper()
		if got.Get(md.Fields().ByName("agent_version")).String() != "test" ||
			got.Get(md.Fields().ByName("cpu_cores")).Int() != 8 ||
			!got.Get(md.Fields().ByName("self_check_ok")).Bool() {
			t.Fatalf("legacy fields lost: %v", got)
		}
		checks := got.Get(md.Fields().ByName("checks")).List()
		if checks.Len() != 1 {
			t.Fatalf("legacy checks = %v", checks)
		}
		check := checks.Get(0).Message()
		if check.Get(check.Descriptor().Fields().ByName("name")).String() != "posture: auditd running" ||
			check.Get(check.Descriptor().Fields().ByName("ok")).Bool() ||
			check.Get(check.Descriptor().Fields().ByName("detail")).String() != "not running" {
			t.Fatalf("legacy check changed: %v", check)
		}
	}
	t.Run("new sender to old protobuf receiver", func(t *testing.T) {
		wire, err := proto.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		old := dynamicpb.NewMessage(md)
		if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(wire, old); err != nil {
			t.Fatal(err)
		}
		assertLegacy(t, old)
	})
	t.Run("new REST sender to old default gateway", func(t *testing.T) {
