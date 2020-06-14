package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeActuation records the host-bearer metadata it saw on Heartbeat.
type fakeActuation struct {
	cloudv1.UnimplementedActuationServiceServer
	mu              sync.Mutex
	bearer          string
	beats           int
	reportedID      string
	reportedState   string
	enrollToken     string
	enrollHostID    string
	enrollDriverTok string
	enrollBackendID string
	// enrollAdoptForeign records the #1006 acknowledgement flag, so a test can
	// prove `cloud enroll --adopt-foreign` actually reaches the control plane.
	enrollAdoptForeign bool
	// enrollHostIDOverride, when set, makes EnrollHost return this id instead
	// of the join token's own embedded id — simulating a cloud #572 re-enroll,
	// where the cloud resolves to a pre-existing host row with a different id.
	enrollHostIDOverride string
	statusBearer         string
	statusReq            *cloudv1.ReportHostStatusRequest
	statusReports        int

	// sshTrustVersion/sshTrustKeys configure the canned SSH CA trust bundle.
	// Zero value = "no SSH CA configured": Heartbeat leaves ssh_trust_version
	// empty and the client is expected to never call GetSSHTrustBundle.
	sshTrustVersion      string
	sshTrustKeys         []*cloudv1.SSHCAKey
	sshTrustCalls        int
	authorizeReq         *pb.AuthorizeSSHRequest
	authorizeBearer      string
	authorizeHasDeadline bool
	authorizeResp        *pb.AuthorizeSSHResponse
	authorizeErr         error
}

// EnrollHost echoes back the host id embedded in the join token (first
// dotted segment) so the client's Enroll returns it.
func (f *fakeActuation) EnrollHost(_ context.Context, req *cloudv1.EnrollHostRequest) (*cloudv1.EnrollHostResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrollToken = req.GetJoinToken()
	f.enrollDriverTok = req.GetDriverToken()
	f.enrollBackendID = req.GetOssBackendId()
	f.enrollAdoptForeign = req.GetAdoptForeign()
	id := req.GetJoinToken()
	if i := indexByte(id, '.'); i >= 0 {
		id = id[:i]
	}
	if f.enrollHostIDOverride != "" {
		id = f.enrollHostIDOverride
	}
	f.enrollHostID = id
	return &cloudv1.EnrollHostResponse{HostId: id}, nil
}

// ReportHostStatus records the capability report + the bearer it arrived with.
func (f *fakeActuation) ReportHostStatus(ctx context.Context, req *cloudv1.ReportHostStatusRequest) (*cloudv1.ReportHostStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusReports++
	f.statusReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(hostBearerMetadataKey); len(v) > 0 {
			f.statusBearer = v[0]
		}
	}
	return &cloudv1.ReportHostStatusResponse{}, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func (f *fakeActuation) Heartbeat(ctx context.Context, _ *cloudv1.HeartbeatRequest) (*cloudv1.HeartbeatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beats++
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(hostBearerMetadataKey); len(v) > 0 {
			f.bearer = v[0]
		}
	}
	return &cloudv1.HeartbeatResponse{SshTrustVersion: f.sshTrustVersion}, nil
}

func (f *fakeActuation) AuthorizeSSH(ctx context.Context, req *pb.AuthorizeSSHRequest) (*pb.AuthorizeSSHResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizeReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(hostBearerMetadataKey); len(v) > 0 {
			f.authorizeBearer = v[0]
		}
	}
	_, f.authorizeHasDeadline = ctx.Deadline()
	if f.authorizeResp == nil && f.authorizeErr == nil {
		return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW}, nil
	}
	return f.authorizeResp, f.authorizeErr
}

// GetSSHTrustBundle mirrors the real server's known_version short-circuit
// (internal/server/actuation_server.go's GetSSHTrustBundle on the cloud
// side) so tests exercise the real protocol, not just canned responses.
func (f *fakeActuation) GetSSHTrustBundle(_ context.Context, req *cloudv1.GetSSHTrustBundleRequest) (*cloudv1.GetSSHTrustBundleResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sshTrustCalls++
	if known := req.GetKnownVersion(); known != "" && known == f.sshTrustVersion {
		return &cloudv1.GetSSHTrustBundleResponse{Unchanged: true, Version: f.sshTrustVersion}, nil
	}
	return &cloudv1.GetSSHTrustBundleResponse{Version: f.sshTrustVersion, CaKeys: f.sshTrustKeys}, nil
}

// WatchAssignments sends one batch (with the canned policies) then closes the
// stream, so watchOnce reconciles once and returns cleanly.
func (f *fakeActuation) WatchAssignments(_ *cloudv1.WatchAssignmentsRequest, stream cloudv1.ActuationService_WatchAssignmentsServer) error {
	return stream.Send(&cloudv1.AssignmentBatch{
		NetworkPolicies: []*cloudv1.NetworkPolicy{
			{OrgId: "org-1", EgressCidrs: []string{"8.8.8.8/32"}, Mode: cloudv1.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE},
		},
	})
}

// ReportContainerState records what the client reported.
func (f *fakeActuation) ReportContainerState(ctx context.Context, req *cloudv1.ReportContainerStateRequest) (*cloudv1.ReportContainerStateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reportedID = req.GetContainerId()
	f.reportedState = req.GetState()
	return &cloudv1.ReportContainerStateResponse{}, nil
}

// fakeActuator records the container actions the reconciler drove.
type fakeActuator struct {
	mu      sync.Mutex
	running []ContainerSpec
	stopped []string
	deleted []string
}

func (a *fakeActuator) EnsureRunning(_ context.Context, s ContainerSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = append(a.running, s)
	return nil
}
func (a *fakeActuator) EnsureStopped(_ context.Context, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = append(a.stopped, name)
	return nil
}
func (a *fakeActuator) EnsureDeleted(_ context.Context, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleted = append(a.deleted, name)
	return nil
}

// recordingSink captures the policies handed to it.
type recordingSink struct {
	mu       sync.Mutex
	policies []*cloudv1.NetworkPolicy
	calls    int
}

func (s *recordingSink) SyncNetworkPolicies(_ context.Context, p []*cloudv1.NetworkPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.policies = p
	return nil
}

// newTestClient wires a Client to a bufconn-backed fake server, bypassing dial.
func newTestClient(t *testing.T, cfg *Config) (*Client, *fakeActuation) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	fake := &fakeActuation{}
	cloudv1.RegisterActuationServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	gc := cloudv1.NewActuationServiceClient(conn)
	c := &Client{cfg: cfg, interval: defaultHeartbeatInterval, ac: gc, watchAC: gc, sshAC: gc}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.cancel)
	return c, fake
}

func TestAuthorizeSSHSendsBearerAndDeadline(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.secretbearer"}
	c, fake := newTestClient(t, cfg)
	req := &pb.AuthorizeSSHRequest{Login: "alice", SentinelId: "sentinel-1"}
	resp, err := c.AuthorizeSSH(context.Background(), req)
	if err != nil {
		t.Fatalf("AuthorizeSSH: %v", err)
	}
	if resp.GetDecision() != pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW {
		t.Fatalf("decision = %v, want ALLOW", resp.GetDecision())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !proto.Equal(fake.authorizeReq, req) {
		t.Fatalf("request changed: got %v, want %v", fake.authorizeReq, req)
	}
	if fake.authorizeBearer != cfg.Token {
		t.Fatalf("server saw bearer %q, want %q", fake.authorizeBearer, cfg.Token)
	}
	if !fake.authorizeHasDeadline {
		t.Fatal("AuthorizeSSH request has no deadline")
	}
}

func TestHeartbeatSendsHostBearer(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.secretbearer"}
	c, fake := newTestClient(t, cfg)

	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.beats != 1 {
		t.Errorf("expected 1 heartbeat, got %d", fake.beats)
	}
	if fake.bearer != "host-1.secretbearer" {
		t.Errorf("server saw bearer %q, want the configured token", fake.bearer)
	}
}

// TestHeartbeatFetchesTrustBundleOnNewVersion is the core #1928 regression
// guard: a heartbeat that advertises a new ssh_trust_version must cause the
// client to fetch and cache the bundle, and a SUBSEQUENT heartbeat at the
// SAME version must NOT re-fetch (the whole point of the version hint is a
// near-free poll once converged).
func TestHeartbeatFetchesTrustBundleOnNewVersion(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.secretbearer"}
	c, fake := newTestClient(t, cfg)
	fake.sshTrustVersion = "v1"
	fake.sshTrustKeys = []*cloudv1.SSHCAKey{
		{KeyId: "ca-1", PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA", Active: true},
	}

	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce: %v", err)
	}

	content, version, ok := c.SSHTrustedUserCAKeys()
	if !ok {
		t.Fatal("SSHTrustedUserCAKeys: ok = false, want true after a heartbeat advertising a new version")
	}
	if version != "v1" {
		t.Errorf("version = %q, want %q", version, "v1")
	}
	if !strings.Contains(content, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA") {
		t.Errorf("content = %q, want it to contain the CA public key", content)
	}
	if !strings.Contains(content, "kid=ca-1") {
		t.Errorf("content = %q, want the key-id comment for operator visibility", content)
	}

	fake.mu.Lock()
	calls := fake.sshTrustCalls
	fake.mu.Unlock()
	if calls != 1 {
		t.Fatalf("GetSSHTrustBundle calls = %d, want 1 before the second heartbeat", calls)
	}

	// Second heartbeat at the same version: no re-fetch.
	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce (2nd): %v", err)
	}
	fake.mu.Lock()
	calls = fake.sshTrustCalls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("GetSSHTrustBundle calls after 2nd heartbeat at the same version = %d, want still 1 (short-circuit)", calls)
	}
}

// TestHeartbeatSkipsTrustFetchWhenNoCAConfigured covers a cloud daemon with
// no SSH CA at all: Heartbeat leaves ssh_trust_version empty, and the client
// must never call GetSSHTrustBundle (which would 501 Unimplemented on such a
// daemon per the real server — see actuation_server.go).
func TestHeartbeatSkipsTrustFetchWhenNoCAConfigured(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.secretbearer"}
	c, fake := newTestClient(t, cfg)

	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce: %v", err)
	}

	if _, _, ok := c.SSHTrustedUserCAKeys(); ok {
		t.Error("SSHTrustedUserCAKeys: ok = true, want false when the cloud daemon has no SSH CA configured")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.sshTrustCalls != 0 {
		t.Errorf("GetSSHTrustBundle calls = %d, want 0", fake.sshTrustCalls)
	}
}

// TestRefreshSSHTrustBundleIfStale_EmptyCaKeysRefused pins the safety
// refusal: a non-unchanged response with zero CA keys (a cloud-side bug, or
// a wire hiccup) must NOT overwrite a good cached bundle — installing an
// empty TrustedUserCAKeys file would trust nothing and lock out every
// certificate-auth user, which is worse than serving stale-but-working trust.
func TestRefreshSSHTrustBundleIfStale_EmptyCaKeysRefused(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.secretbearer"}
	c, fake := newTestClient(t, cfg)
	fake.sshTrustVersion = "v1"
	fake.sshTrustKeys = []*cloudv1.SSHCAKey{{KeyId: "ca-1", PublicKey: "ssh-ed25519 AAAA", Active: true}}
	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce (seed v1): %v", err)
	}
	seededContent, _, _ := c.SSHTrustedUserCAKeys()

	// Cloud rotates to v2 but (bug/hiccup) serves no keys for it.
	fake.mu.Lock()
	fake.sshTrustVersion = "v2"
	fake.sshTrustKeys = nil
	fake.mu.Unlock()
	if err := c.heartbeatOnce(context.Background()); err != nil {
		t.Fatalf("heartbeatOnce (v2, empty keys): %v", err)
	}

	content, version, ok := c.SSHTrustedUserCAKeys()
	if !ok {
		t.Fatal("SSHTrustedUserCAKeys: ok = false, want the v1 cache to survive")
	}
	if version != "v1" {
		t.Errorf("version = %q, want the refusal to keep %q", version, "v1")
	}
	if content != seededContent {
		t.Errorf("content changed despite the empty-keys refusal: got %q, want %q", content, seededContent)
	}
}

func TestRenderTrustedUserCAKeysFile(t *testing.T) {
	got := renderTrustedUserCAKeysFile([]*cloudv1.SSHCAKey{
		{KeyId: "ca-1", PublicKey: "  ssh-ed25519 AAAAactive  \n", Active: true},
		{KeyId: "ca-0", PublicKey: "ssh-ed25519 AAAAretired", Active: false},
	})
	want := "# kid=ca-1 (active)\nssh-ed25519 AAAAactive\n# kid=ca-0 (retired)\nssh-ed25519 AAAAretired\n"
	if got != want {
		t.Errorf("renderTrustedUserCAKeysFile() =\n%q\nwant\n%q", got, want)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(&Config{HostID: "h", Token: "t"}, Deps{}); err == nil {
		t.Error("New must reject a config missing control_plane")
	}
}

func TestWatchOnceSyncsNetworkPolicies(t *testing.T) {
	cfg := &Config{ControlPlane: "bufconn", HostID: "host-1", Token: "host-1.bearer"}
	c, _ := newTestClient(t, cfg)
	sink := &recordingSink{}
	c.sink = sink

	// watchOnce reconciles the one batch the fake sends, then returns on EOF.
	_ = c.watchOnce()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.calls == 0 {
		t.Fatal("sink never received a batch")
	}
	if len(sink.policies) != 1 || sink.policies[0].GetOrgId() != "org-1" {
		t.Fatalf("sink got wrong policies: %+v", sink.policies)
	}
	if sink.policies[0].GetMode() != cloudv1.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE {
		t.Errorf("mode not propagated: %v", sink.policies[0].GetMode())
	}
}

