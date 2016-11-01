package codeegress

import (
	"errors"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestPolicyPath(t *testing.T) {
	cases := map[string]string{
		"":          "/v1/code/egress-policy",
		"alice":     "/v1/code/egress-policy/alice",
		"a b/c":     "/v1/code/egress-policy/a%20b%2Fc",
		"  alice  ": "/v1/code/egress-policy/alice",
	}
	for tenant, want := range cases {
		if got := PolicyPath(tenant); got != want {
			t.Errorf("PolicyPath(%q) = %q, want %q", tenant, got, want)
		}
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    pb.NetworkPolicyMode
		wantErr bool
	}{
		{"log_only", logOnly, false},
		{"LOG-ONLY", logOnly, false},
		{"enforce", enforce, false},
		{"NETWORK_POLICY_MODE_ENFORCE", enforce, false},
		{"", 0, true},
		{"drop", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v, err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

type fakeAPI struct {
	gotGet *pb.GetCodingToolEgressPolicyRequest
	gotSet *pb.SetCodingToolEgressPolicyRequest
	resp   *pb.GetCodingToolEgressPolicyResponse
	err    error
}

func (f *fakeAPI) GetCodingToolEgressPolicy(r *pb.GetCodingToolEgressPolicyRequest) (*pb.GetCodingToolEgressPolicyResponse, error) {
	f.gotGet = r
	return f.resp, f.err
}

func (f *fakeAPI) SetCodingToolEgressPolicy(r *pb.SetCodingToolEgressPolicyRequest) (*pb.SetCodingToolEgressPolicyResponse, error) {
	f.gotSet = r
	return &pb.SetCodingToolEgressPolicyResponse{Policy: r.GetPolicy()}, f.err
}

func TestGetAndSet_ForwardTypedRequests(t *testing.T) {
	f := &fakeAPI{resp: &pb.GetCodingToolEgressPolicyResponse{}}
	if _, err := Get(f, " alice "); err != nil || f.gotGet.GetTenant() != "alice" {
		t.Fatalf("Get forwarded %+v, %v", f.gotGet, err)
	}
