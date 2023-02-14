package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBackend simulates a remote containarium daemon.
// It maintains an in-memory container store and handles the REST API endpoints.
type fakeBackend struct {
	id         string
	mu         sync.RWMutex
	containers []fakeContainer
	systemInfo fakeSystemInfo
	// Track received requests for assertions
	requests []fakeRequest
}

type fakeContainer struct {
	Name      string            `json:"name"`
	Username  string            `json:"username"`
	State     string            `json:"state"`
	Resources map[string]string `json:"resources"`
	Network   map[string]string `json:"network"`
	Labels    map[string]string `json:"labels"`
	GPU       string            `json:"gpuDevice"`
	BackendID string            `json:"backendId"`
}

type fakeSystemInfo struct {
	Hostname  string    `json:"hostname"`
	TotalCPUs int       `json:"totalCpus"`
	TotalMem  int64     `json:"totalMemoryBytes"`
	GPUs      []fakeGPU `json:"gpus"`
}

type fakeGPU struct {
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	ModelName     string `json:"modelName"`
	DriverVersion string `json:"driverVersion"`
	CUDAVersion   string `json:"cudaVersion"`
}

type fakeRequest struct {
	Method string
	Path   string
	Body   string
}

func newFakeBackend(id string, containers []fakeContainer, sysInfo fakeSystemInfo) *fakeBackend {
	return &fakeBackend{
		id:         id,
		containers: containers,
		systemInfo: sysInfo,
	}
}

func (fb *fakeBackend) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		fb.mu.Lock()
		fb.requests = append(fb.requests, fakeRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   string(bodyBytes),
		})
		fb.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch {
		// List containers
		case r.Method == "GET" && r.URL.Path == "/v1/containers":
			fb.mu.RLock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"containers": fb.containers,
			})
			fb.mu.RUnlock()

		// Get system info
		case r.Method == "GET" && r.URL.Path == "/v1/system/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"info": fb.systemInfo,
			})

		// Create container
		case r.Method == "POST" && r.URL.Path == "/v1/containers":
			var req struct {
				Username string `json:"username"`
				GPU      string `json:"gpu"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			c := fakeContainer{
				Name:      req.Username + "-container",
				Username:  req.Username,
				State:     "CONTAINER_STATE_RUNNING",
				Resources: map[string]string{"cpu": "4", "memory": "8GB", "disk": "50GB"},
				Network:   map[string]string{"ipAddress": "10.0.3.200"},
				GPU:       req.GPU,
			}
			fb.mu.Lock()
			fb.containers = append(fb.containers, c)
			fb.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"container": c,
				"message":   "created",
			})

		// Resize
		case r.Method == "PUT" && len(r.URL.Path) > len("/v1/containers/") && pathEndsWith(r.URL.Path, "/resize"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "resized",
			})

		// Start
		case r.Method == "POST" && pathEndsWith(r.URL.Path, "/start"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "started",
			})

		// Stop
		case r.Method == "POST" && pathEndsWith(r.URL.Path, "/stop"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "stopped",
			})

		// Delete
		case r.Method == "DELETE" && len(r.URL.Path) > len("/v1/containers/"):
			username := extractUsername(r.URL.Path)
			fb.mu.Lock()
			filtered := fb.containers[:0]
			for _, c := range fb.containers {
				if c.Username != username {
					filtered = append(filtered, c)
				}
			}
			fb.containers = filtered
			fb.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "deleted",
			})

		// Cleanup disk
		case r.Method == "POST" && pathEndsWith(r.URL.Path, "/cleanup-disk"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message":    "disk cleaned",
				"freedBytes": 1024000,
			})

		// Collaborators
		case r.Method == "GET" && pathEndsWith(r.URL.Path, "/collaborators"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"collaborators": []map[string]string{},
				"totalCount":    0,
			})
		case r.Method == "POST" && pathEndsWith(r.URL.Path, "/collaborators"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "collaborator added",
			})
		case r.Method == "DELETE" && pathContains(r.URL.Path, "/collaborators/"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "collaborator removed",
			})

		default:
			http.NotFound(w, r)
		}
	})
}

func (fb *fakeBackend) requestCount() int {
	fb.mu.RLock()
	defer fb.mu.RUnlock()
	return len(fb.requests)
}

func (fb *fakeBackend) lastRequest() fakeRequest {
	fb.mu.RLock()
	defer fb.mu.RUnlock()
	if len(fb.requests) == 0 {
		return fakeRequest{}
	}
	return fb.requests[len(fb.requests)-1]
}

// --- Integration Tests ---

// TestIntegration_MultiBackendListContainers verifies that ListContainers
// fans out to all healthy peers and merges results with backend_id tagging.
func TestIntegration_MultiBackendListContainers(t *testing.T) {
	// Backend 1: GCP spot with 2 containers
	spot := newFakeBackend("gcp-spot", []fakeContainer{
		{Name: "alice-container", Username: "alice", State: "CONTAINER_STATE_RUNNING"},
		{Name: "bob-container", Username: "bob", State: "CONTAINER_STATE_STOPPED"},
	}, fakeSystemInfo{})

	// Backend 2: GPU node with 1 container
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING", GPU: "0"},
	}, fakeSystemInfo{})

	spotSrv := httptest.NewServer(spot.handler())
	defer spotSrv.Close()
	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["gcp-spot"] = &PeerClient{
		ID: "gcp-spot", Addr: spotSrv.Listener.Addr().String(),
		Healthy: true, client: spotSrv.Client(),
	}
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	containers, _ := pool.ListContainers("")

	if len(containers) != 3 {
		t.Fatalf("expected 3 containers across 2 backends, got %d", len(containers))
	}

	// Verify backend_id tagging
	backendCounts := make(map[string]int)
	for _, c := range containers {
		backendCounts[c.BackendID]++
	}
	if backendCounts["gcp-spot"] != 2 {
		t.Errorf("expected 2 containers from gcp-spot, got %d", backendCounts["gcp-spot"])
	}
	if backendCounts["tunnel-gpu"] != 1 {
		t.Errorf("expected 1 container from tunnel-gpu, got %d", backendCounts["tunnel-gpu"])
	}
}

// TestIntegration_PeerForwardResize verifies that resize operations
// are forwarded to the correct peer when the container is not local.
func TestIntegration_PeerForwardResize(t *testing.T) {
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING"},
	}, fakeSystemInfo{})

	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	// Find charlie's peer
	peer, _ := pool.FindContainerPeer("charlie", "")
	if peer == nil {
		t.Fatal("expected to find charlie on tunnel-gpu")
	}
	if peer.ID != "tunnel-gpu" {
		t.Errorf("expected peer ID 'tunnel-gpu', got %q", peer.ID)
	}

	// Forward resize
	body := []byte(`{"cpu":"8","memory":"16GB","disk":"100GB"}`)
	respBody, statusCode, err := peer.ForwardRequest("PUT", "/v1/containers/charlie/resize", "", body)
	if err != nil {
		t.Fatalf("resize forward failed: %v", err)
	}
	if statusCode != 200 {
		t.Errorf("expected 200, got %d: %s", statusCode, respBody)
	}

	// Verify request reached the peer
	lastReq := gpu.lastRequest()
	if lastReq.Method != "PUT" || lastReq.Path != "/v1/containers/charlie/resize" {
		t.Errorf("unexpected request: %s %s", lastReq.Method, lastReq.Path)
	}
}

// TestIntegration_ResizeContainerForwardsRequestFields verifies that
// ContainerServer.ResizeContainer's peer-forward path (the container isn't
// local, so the request is forwarded over HTTP) carries CpuRequest and
// MemoryRequest along with cpu/memory/disk. The forward body used to be
// built from a map literal naming only cpu/memory/disk, silently dropping
// the two request fields on any resize routed through a peer (#1572,
// caught in review on PR #1577).
func TestIntegration_ResizeContainerForwardsRequestFields(t *testing.T) {
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING"},
	}, fakeSystemInfo{})
	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	// The container is not local: GetContainer errors, so
	// container.Manager.Resize returns "not found" and ResizeContainer
	// falls through to the peer-forward path above.
	mock := incustest.NewMockBackend()
	mock.GetContainerFunc = func(name string) (*incus.ContainerInfo, error) {
		return nil, fmt.Errorf("not found")
	}
	s := &ContainerServer{
		manager:  container.NewWithBackend(mock),
		peerPool: pool,
	}

	ctx := auth.ContextWithTestSubjectScopes(context.Background(),
		"charlie", []string{"user"}, []string{auth.ScopeContainersWrite})

	_, err := s.ResizeContainer(ctx, &pb.ResizeContainerRequest{
		Username:      "charlie",
		Cpu:           "4",
		CpuRequest:    "500m",
		MemoryRequest: "1Gi",
	})
	if err != nil {
		t.Fatalf("ResizeContainer: %v", err)
	}

	lastReq := gpu.lastRequest()
	if lastReq.Method != "PUT" || lastReq.Path != "/v1/containers/charlie/resize" {
		t.Fatalf("unexpected request: %s %s", lastReq.Method, lastReq.Path)
	}
	var forwarded map[string]string
	if err := json.Unmarshal([]byte(lastReq.Body), &forwarded); err != nil {
		t.Fatalf("forwarded body not valid JSON: %v (%s)", err, lastReq.Body)
	}
	if forwarded["cpuRequest"] != "500m" {
		t.Errorf("forwarded cpuRequest = %q, want %q (raw body: %s)", forwarded["cpuRequest"], "500m", lastReq.Body)
	}
	if forwarded["memoryRequest"] != "1Gi" {
		t.Errorf("forwarded memoryRequest = %q, want %q (raw body: %s)", forwarded["memoryRequest"], "1Gi", lastReq.Body)
	}
}

// TestIntegration_ResizeAdmissionNotEvaluatedForPeerForwardedContainer proves
// the #1579 requirement that a peer-forwarded resize is never evaluated
// against the LOCAL host's capacity. The local gate here is configured to
// reject absolutely everything (factor effectively 0 capacity headroom) —
// if the admission check ran unconditionally before the local/peer dispatch
// decision (rather than only once the container is confirmed local), this
// resize would be wrongly rejected with ResourceExhausted before ever
// reaching the peer. It must instead sail through to the peer unblocked.
func TestIntegration_ResizeAdmissionNotEvaluatedForPeerForwardedContainer(t *testing.T) {
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING"},
	}, fakeSystemInfo{})
	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	// charlie's box is not local: GetContainer errors so ResizeContainer
	// falls through to the peer-forward path.
	mock := incustest.NewMockBackend()
	mock.GetContainerFunc = func(name string) (*incus.ContainerInfo, error) {
		return nil, fmt.Errorf("not found")
	}
	s := &ContainerServer{
		manager:  container.NewWithBackend(mock),
		peerPool: pool,
	}
	// A maximally strict local gate: 0 physical cores of headroom would
	// reject any nonzero CPU request outright if evaluated against THIS
	// host. hostCoresFn reports a nonzero host (so the gate doesn't fail
	// open on "capacity unknown") with zero factor-adjusted ceiling.
	s.hostCoresFn = func() (float64, error) { return 8, nil }
	s.SetCPUOvercommitPolicy(0.0001, true) // effectively zero headroom, strictly enforced

	ctx := auth.ContextWithTestSubjectScopes(context.Background(),
		"charlie", []string{"user"}, []string{auth.ScopeContainersWrite})

	_, err := s.ResizeContainer(ctx, &pb.ResizeContainerRequest{Username: "charlie", Cpu: "8"})
	if err != nil {
		t.Fatalf("peer-forwarded resize must not be evaluated against local capacity, got %v", err)
	}
	if status.Code(err) == codes.ResourceExhausted {
		t.Fatalf("resize was rejected by the LOCAL admission gate despite the container living on a peer")
	}

	lastReq := gpu.lastRequest()
	if lastReq.Method != "PUT" || lastReq.Path != "/v1/containers/charlie/resize" {
		t.Fatalf("resize never reached the peer: %s %s", lastReq.Method, lastReq.Path)
	}
}

// TestIntegration_PeerForwardDelete verifies delete operations forward correctly.
func TestIntegration_PeerForwardDelete(t *testing.T) {
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING"},
	}, fakeSystemInfo{})

	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	peer, _ := pool.FindContainerPeer("charlie", "")
	if peer == nil {
		t.Fatal("expected to find charlie")
	}

	_, statusCode, err := peer.ForwardRequest("DELETE", "/v1/containers/charlie", "", nil)
	if err != nil {
		t.Fatalf("delete forward failed: %v", err)
	}
	if statusCode != 200 {
		t.Errorf("expected 200, got %d", statusCode)
	}

	// Verify container was removed from fake backend
	gpu.mu.RLock()
	if len(gpu.containers) != 0 {
		t.Errorf("expected 0 containers after delete, got %d", len(gpu.containers))
	}
	gpu.mu.RUnlock()
}

// TestIntegration_PeerForwardCollaborators verifies collaborator operations forward correctly.
func TestIntegration_PeerForwardCollaborators(t *testing.T) {
	gpu := newFakeBackend("tunnel-gpu", []fakeContainer{
		{Name: "charlie-container", Username: "charlie", State: "CONTAINER_STATE_RUNNING"},
	}, fakeSystemInfo{})

	gpuSrv := httptest.NewServer(gpu.handler())
	defer gpuSrv.Close()

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-gpu"] = &PeerClient{
		ID: "tunnel-gpu", Addr: gpuSrv.Listener.Addr().String(),
		Healthy: true, client: gpuSrv.Client(),
	}
	pool.mu.Unlock()

	peer, _ := pool.FindContainerPeer("charlie", "")
	if peer == nil {
		t.Fatal("expected to find charlie")
	}

	// Add collaborator
	body := []byte(`{"collaborator_username":"dave","ssh_public_key":"ssh-ed25519 AAAA_dave"}`)
	_, statusCode, err := peer.ForwardRequest("POST", "/v1/containers/charlie/collaborators", "", body)
	if err != nil {
		t.Fatalf("add collaborator failed: %v", err)
	}
	if statusCode != 200 {
		t.Errorf("expected 200, got %d", statusCode)
	}

	// List collaborators
	_, statusCode, err = peer.ForwardRequest("GET", "/v1/containers/charlie/collaborators", "", nil)
	if err != nil {
