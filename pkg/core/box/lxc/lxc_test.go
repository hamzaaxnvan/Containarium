package lxc

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// newTestBackend wires an lxc.Backend over a Manager backed by the shared
// incustest.MockBackend, so delegation can be exercised without a real host.
func newTestBackend(mock *incustest.MockBackend) *Backend {
	return New(container.NewWithBackend(mock))
}

func TestKind(t *testing.T) {
	if got := New(nil).Kind(); got != box.KindLXC {
		t.Fatalf("Kind() = %q, want %q", got, box.KindLXC)
	}
}

func TestParseState(t *testing.T) {
	cases := map[string]pb.ContainerState{
		"Running": pb.ContainerState_CONTAINER_STATE_RUNNING,
		"Stopped": pb.ContainerState_CONTAINER_STATE_STOPPED,
		"Frozen":  pb.ContainerState_CONTAINER_STATE_FROZEN,
		"":        pb.ContainerState_CONTAINER_STATE_UNSPECIFIED,
		"weird":   pb.ContainerState_CONTAINER_STATE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := parseState(in); got != want {
			t.Errorf("parseState(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTenantOf(t *testing.T) {
	if got := tenantOf(&incus.ContainerInfo{Username: "alice", Name: "alice-container"}); got != "alice" {
		t.Errorf("tenantOf with Username = %q, want alice", got)
	}
	// Falls back to stripping the -container suffix when Username is empty.
	if got := tenantOf(&incus.ContainerInfo{Name: "bob-container"}); got != "bob" {
		t.Errorf("tenantOf fallback = %q, want bob", got)
	}
}

func TestContainerName(t *testing.T) {
	if got := containerName(box.BoxRef{Tenant: "alice"}); got != "alice-container" {
		t.Errorf("containerName derived = %q, want alice-container", got)
	}
	if got := containerName(box.BoxRef{Tenant: "alice", Name: "explicit"}); got != "explicit" {
		t.Errorf("containerName explicit = %q, want explicit", got)
	}
}

func TestSpecToCreateOptions(t *testing.T) {
	spec := box.BoxSpec{
		Ref:         box.BoxRef{Tenant: "alice"},
		Image:       "ubuntu/24.04",
		OSType:      pb.OSType_OS_TYPE_UBUNTU_2404,
		Resources:   box.ResourceLimits{CPU: "2", Memory: "4GB", Disk: "20GB"},
		GPUs:        []string{"0"},
		SSHKeys:     []string{"ssh-ed25519 AAAA"},
		Labels:      map[string]string{"team": "infra"},
		Monitoring:  true,
		Stack:       "python",
		StackParams: map[string]string{"version": "3.12"},
		AutoStart:   true,
		StoragePool: "containarium-tenant-alice",
	}
	got := specToCreateOptions(spec)
	want := container.CreateOptions{
		Username:        "alice",
		Image:           "ubuntu/24.04",
		CPU:             "2",
		Memory:          "4GB",
		Disk:            "20GB",
		GPUs:            []string{"0"},
		SSHKeys:         []string{"ssh-ed25519 AAAA"},
		Labels:          map[string]string{"team": "infra"},
		OSType:          pb.OSType_OS_TYPE_UBUNTU_2404,
		Monitoring:      true,
		Stack:           "python",
		StackParameters: map[string]string{"version": "3.12"},
		AutoStart:       true,
		StoragePool:     "containarium-tenant-alice",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("specToCreateOptions mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestInfoToStatus(t *testing.T) {
	info := &incus.ContainerInfo{
		Name:      "alice-container",
		Username:  "alice",
		State:     "Running",
		IPAddress: "10.0.0.5",
		CPU:       "2",
		Memory:    "4GB",
		Disk:      "20GB",
		Labels:    map[string]string{"team": "infra"},
		BackendID: "node-a",
	}
	st := StatusFromInfo(info)
	if st.Ref != (box.BoxRef{Tenant: "alice", Name: "alice-container"}) {
		t.Errorf("Ref = %+v", st.Ref)
	}
	if st.State != pb.ContainerState_CONTAINER_STATE_RUNNING {
		t.Errorf("State = %v", st.State)
	}
	if st.IPAddress != "10.0.0.5" {
		t.Errorf("IPAddress = %q", st.IPAddress)
	}
	if st.Resources != (box.ResourceLimits{CPU: "2", Memory: "4GB", Disk: "20GB"}) {
		t.Errorf("Resources = %+v", st.Resources)
	}
	if st.BackendID != "node-a" || !reflect.DeepEqual(st.Labels, info.Labels) {
		t.Errorf("BackendID/Labels = %q / %+v", st.BackendID, st.Labels)
	}
}

// TestInfoToStatus_Stack: the stack recorded at create rides ContainerInfo
// onto BoxStatus (#2163); an unrecorded stack stays empty.
func TestInfoToStatus_Stack(t *testing.T) {
	if got := StatusFromInfo(&incus.ContainerInfo{Name: "a-container", Stack: "nodejs"}).Stack; got != "nodejs" {
		t.Errorf("Stack = %q, want nodejs", got)
	}
	if got := StatusFromInfo(&incus.ContainerInfo{Name: "a-container"}).Stack; got != "" {
		t.Errorf("Stack = %q, want empty for a box with no recorded stack", got)
	}
}

// TestInfoToStatus_PodmanEnabled (#2858): the flag rides ContainerInfo onto
// BoxStatus unchanged in both directions.
func TestInfoToStatus_PodmanEnabled(t *testing.T) {
	for _, want := range []bool{true, false} {
		if got := StatusFromInfo(&incus.ContainerInfo{Name: "a-container", PodmanEnabled: want}).PodmanEnabled; got != want {
			t.Errorf("PodmanEnabled = %v, want %v", got, want)
		}
	}
}

func TestLifecycleDelegation(t *testing.T) {
	mock := incustest.NewMockBackend()
	mock.Containers["alice-container"] = &incus.ContainerInfo{
		Name:      "alice-container",
		State:     "Stopped",
		IPAddress: "10.0.0.5",
	}
	b := newTestBackend(mock)
	ctx := context.Background()
	ref := box.BoxRef{Tenant: "alice"}

	// Get
	st, err := b.Get(ctx, ref)
	if err != nil || st == nil {
		t.Fatalf("Get returned (%v, %v)", st, err)
	}
	if st.Ref.Tenant != "alice" || st.IPAddress != "10.0.0.5" {
		t.Errorf("Get status = %+v", st)
	}

	// Get of a missing box → (nil, nil)
	if missing, err := b.Get(ctx, box.BoxRef{Tenant: "ghost"}); err != nil || missing != nil {
		t.Errorf("Get(missing) = (%v, %v), want (nil, nil)", missing, err)
	}

	// List
	list, err := b.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List returned (%d, %v)", len(list), err)
	}

	// Start / Stop toggle the mock state.
	if err := b.Start(ctx, ref); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mock.Containers["alice-container"].State != "Running" {
		t.Errorf("after Start, state = %q", mock.Containers["alice-container"].State)
	}
	if err := b.Stop(ctx, ref, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if mock.Containers["alice-container"].State != "Stopped" {
		t.Errorf("after Stop, state = %q", mock.Containers["alice-container"].State)
	}

	// Resolve mirrors Get's endpoint.
	ep, err := b.Resolve(ctx, ref)
	if err != nil || ep == nil || ep.DirectIP != "10.0.0.5" || ep.SSHUser != "alice" {
		t.Errorf("Resolve = (%+v, %v)", ep, err)
	}

	// Delete removes it.
	if err := b.Delete(ctx, ref, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := mock.Containers["alice-container"]; ok {
		t.Errorf("after Delete, container still present")
