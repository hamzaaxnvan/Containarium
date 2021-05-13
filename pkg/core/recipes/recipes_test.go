package recipes

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestEmbeddedCatalogLoads(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if len(m.List()) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	for _, id := range []string{"ollama", "llamacpp"} {
		r, err := m.Get(id)
		if err != nil {
			t.Errorf("expected built-in recipe %q: %v", id, err)
			continue
		}
		if r.Image == "" {
			t.Errorf("recipe %q has empty image", id)
		}
		if !r.RequiresGpu {
			t.Errorf("recipe %q expected requires_gpu=true", id)
		}
	}
}

func TestAgentWorkspaceRecipe(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	r, err := m.Get("agent-workspace")
	if err != nil {
		t.Fatalf("expected built-in recipe agent-workspace: %v", err)
	}
	if r.RequiresGpu {
		t.Error("agent-workspace should not require a GPU")
	}
	// The exposed port is the in-box auth proxy (:8080), not OpenHands directly.
	if len(r.Ports) != 1 || r.Ports[0].ContainerPort != 8080 {
		t.Errorf("agent-workspace should expose the in-box auth proxy on :8080; got %+v", r.Ports)
	}
	// post_start must run OpenHands Agent Canvas bound to localhost, persist
	// conversations in the box, chown the bind mounts, and stand up the in-box
	// basic-auth proxy (all validated live 2026-06-18).
	joined := strings.Join(r.PostStart, "\n")
	for _, want := range []string{
		"openhands/agent-canvas",
		"/opt/openhands-state", // conversations stored inside the box
		":U",                   // bind mount chowned to the non-root container user
		"127.0.0.1:8000:8000",  // OpenHands not directly reachable
		"caddy hash-password",  // password bcrypt-hashed at deploy
		"basic_auth",           // in-box auth proxy
		"ws_auth=",             // session-cookie handoff for seamless iframe auth
		"SameSite=None",        // cookie sent cross-origin from the embedded iframe
		"/opt/wsauth/token",    // token persisted for the daemon to vend
		"/__ws_login",          // zero-click bootstrap route
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("agent-workspace post_start missing %q", want)
		}
	}
	// The auth password is required (a box is never exposed without it).
	params := map[string]*pb.RecipeParam{}
	for _, p := range r.Parameters {
		params[p.Name] = p
	}
	if pw := params["auth_password"]; pw == nil {
		t.Fatal("agent-workspace should declare an auth_password parameter")
	} else {
		if !pw.Required {
			t.Error("auth_password must be required (box never exposed without auth)")
		}
		if pw.Type != "password" {
			t.Errorf("auth_password type: got %q want password", pw.Type)
		}
	}
	if params["openhands_version"] == nil || params["openhands_version"].Default == "" {
		t.Error("agent-workspace should pin openhands_version to a default tag")
	}
}

func TestAgentRuntimeBrowserRecipe(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	r, err := m.Get("agent-runtime-browser")
	if err != nil {
		t.Fatalf("expected built-in recipe agent-runtime-browser: %v", err)
	}
	if r.RequiresGpu {
		t.Error("agent-runtime-browser should not require a GPU")
	}
	if r.Image == "" {
		t.Error("agent-runtime-browser has empty image")
	}
	// It's agent-runtime plus a headless browser: the in-box agent loop must
	// still get assembled, and Chromium must actually get installed — not
	// just Playwright's package with no browser binary.
	joined := strings.Join(r.PostStart, "\n")
	for _, want := range []string{
		"install-agent-runtime.sh", // same in-box agent loop assembly as agent-runtime
		"playwright",
		"chromium",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("agent-runtime-browser post_start missing %q", want)
		}
	}
}

func TestOCIServiceRecipe(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	r, err := m.Get("oci-service")
	if err != nil {
		t.Fatalf("expected built-in recipe oci-service: %v", err)
	}
	if r.RequiresGpu {
		t.Error("oci-service should not require a GPU")
	}
	// One stable exposed port regardless of what the image listens on.
	if len(r.Ports) != 1 || r.Ports[0].ContainerPort != 8080 {
		t.Errorf("oci-service should expose box port 8080; got %+v", r.Ports)
	}
	// oci_image is the one required parameter; everything else defaults.
	var img *pb.RecipeParam
	for _, p := range r.Parameters {
		if p.Name == "oci_image" {
			img = p
		}
	}
	if img == nil || !img.Required || img.Default != "" {
		t.Errorf("oci_image must be required with no default; got %+v", img)
	}
	if _, err := ResolveParameters(r, map[string]string{}); err == nil {
		t.Error("resolving without oci_image should fail (required)")
	}
	joined := strings.Join(r.PostStart, "\n")
	for _, want := range []string{
		"--restart=always", // survives box reboot and SSH logout
		"--env-file",       // literal env semantics, never bash-sourced
		"--entrypoint",     // command's first token overrides entrypoint
		"CONTAINARIUM_PARAM_OCI_IMAGE",
		"CONTAINARIUM_PARAM_SERVICE_PORT",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("oci-service post_start missing %q", want)
		}
	}
}

func TestRisingWaveRecipe(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	r, err := m.Get("risingwave")
	if err != nil {
		t.Fatalf("expected built-in recipe risingwave: %v", err)
	}
	if r.RequiresGpu {
		t.Error("risingwave should not require a GPU")
	}
	// Only the HTTP ports are routed. :4566 is Postgres wire protocol and
	// Caddy is an HTTP reverse proxy, so routing it would produce a URL that
	// no psql can use — it is reached over SSH local-forward instead.
	got := map[int32]string{}
	for _, p := range r.Ports {
		got[p.ContainerPort] = p.Subdomain
	}
	if len(r.Ports) != 2 || got[5691] != "dashboard" || got[4560] != "webhook" {
		t.Errorf("risingwave should route only the dashboard (:5691) and webhook (:4560) ports; got %+v", r.Ports)
	}
	if _, routed := got[4566]; routed {
		t.Error("risingwave must not route :4566 -- pgwire cannot traverse an HTTP reverse proxy")
	}

	joined := strings.Join(r.PostStart, "\n")
	for _, want := range []string{
		"single_node",                // one process, embedded SQLite meta + local-FS state
		"--store-directory",          // v3 flag name; NOT --state-store-directory
		"/var/lib/risingwave",        // persisted on the named volume, not the container layer
		"--listen-addr 0.0.0.0:4566", // a loopback bind makes the published port unreachable
		"-p 4566:4566",               // published on the box for SSH local-forward
		"--restart=always",           // survives box reboot and SSH logout
		"/dev/tcp/127.0.0.1/4566",    // readiness gate: boot failure surfaces at deploy time
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("risingwave post_start missing %q", want)
		}
	}

	// Both tuning parameters are optional -- the recipe must deploy with no
	// --param at all.
	params := map[string]*pb.RecipeParam{}
	for _, p := range r.Parameters {
		params[p.Name] = p
	}
	if params["version"] == nil || params["version"].Default == "" {
		t.Error("risingwave should pin a default version tag")
	}
	for _, name := range []string{"total_memory_bytes", "parallelism"} {
		if p := params[name]; p == nil {
			t.Errorf("risingwave should declare a %q parameter", name)
		} else if p.Required {
			t.Errorf("%q must be optional (auto-detect when empty)", name)
		}
	}
	if _, err := ResolveParameters(r, map[string]string{}); err != nil {
		t.Errorf("risingwave should resolve with no parameters: %v", err)
	}
}

func TestGetUnknown(t *testing.T) {
	m := New()
	_ = m.LoadEmbedded()
	if _, err := m.Get("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown recipe")
	}
}

func TestLoadRejectsMissingImage(t *testing.T) {
	m := New()
	err := m.LoadFromBytes([]byte("recipes:\n  - id: bad\n"))
	if err == nil {
		t.Fatal("expected error for recipe missing image")
	}
}

func TestLoadRejectsDuplicateID(t *testing.T) {
	m := New()
	yaml := "recipes:\n" +
		"  - id: dup\n    image: a\n" +
		"  - id: dup\n    image: b\n"
	if err := m.LoadFromBytes([]byte(yaml)); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

func TestLoadRejectsBadPort(t *testing.T) {
	m := New()
	yaml := "recipes:\n  - id: x\n    image: a\n    ports:\n      - container_port: 0\n        subdomain: s\n"
	if err := m.LoadFromBytes([]byte(yaml)); err == nil {
		t.Fatal("expected invalid-port error")
	}
}

// #1462: a RecipePort can declare a raw TCP/UDP passthrough instead of an
// HTTP subdomain, for a wire protocol no HTTP client speaks.
func TestLoadPassthroughPort(t *testing.T) {
	m := New()
	yaml := "recipes:\n  - id: x\n    image: a\n    ports:\n" +
		"      - container_port: 4566\n        protocol: tcp\n" +
		"      - container_port: 5432\n        protocol: tcp\n        external_port: 15432\n" +
		"      - container_port: 8080\n        subdomain: web\n"
	if err := m.LoadFromBytes([]byte(yaml)); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	r, err := m.Get("x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(r.Ports) != 3 {
		t.Fatalf("want 3 ports, got %d", len(r.Ports))
	}

	p0 := r.Ports[0]
	if p0.Protocol != pb.RouteProtocol_ROUTE_PROTOCOL_TCP {
		t.Errorf("port 0 protocol = %v, want TCP", p0.Protocol)
	}
	if p0.ExternalPort != 0 {
		t.Errorf("port 0 external_port = %d, want 0 (defaults to container_port at expose time)", p0.ExternalPort)
	}

	p1 := r.Ports[1]
	if p1.Protocol != pb.RouteProtocol_ROUTE_PROTOCOL_TCP || p1.ExternalPort != 15432 {
		t.Errorf("port 1 = %+v, want TCP with external_port 15432", p1)
	}

	p2 := r.Ports[2]
	if p2.Protocol != pb.RouteProtocol_ROUTE_PROTOCOL_UNSPECIFIED || p2.Subdomain != "web" {
		t.Errorf("port 2 = %+v, want unspecified protocol (HTTP default) with subdomain 'web'", p2)
	}
}

func TestLoadPassthroughPortRejectsUnknownProtocol(t *testing.T) {
	m := New()
	yaml := "recipes:\n  - id: x\n    image: a\n    ports:\n      - container_port: 1234\n        protocol: sctp\n"
	if err := m.LoadFromBytes([]byte(yaml)); err == nil {
		t.Fatal("expected error for unknown port protocol")
	}
}

func TestLoadRejectsBadExternalPort(t *testing.T) {
	m := New()
	yaml := "recipes:\n  - id: x\n    image: a\n    ports:\n" +
		"      - container_port: 1234\n        protocol: tcp\n        external_port: 70000\n"
	if err := m.LoadFromBytes([]byte(yaml)); err == nil {
		t.Fatal("expected error for out-of-range external_port")
	}
}

func TestResolveParametersDefaultsAndRequired(t *testing.T) {
	r := &pb.Recipe{
		Id: "r",
		Parameters: []*pb.RecipeParam{
			{Name: "model", Default: "llama3"},
			{Name: "token", Required: true},
		},
	}

	// Missing required → error.
	if _, err := ResolveParameters(r, map[string]string{}); err == nil {
		t.Fatal("expected error when required parameter missing")
	}

	// Override applied, default kept.
	got, err := ResolveParameters(r, map[string]string{"token": "abc", "model": "qwen"})
	if err != nil {
		t.Fatalf("ResolveParameters: %v", err)
	}
	if got["model"] != "qwen" {
		t.Errorf("model override: got %q want qwen", got["model"])
	}
	if got["token"] != "abc" {
		t.Errorf("token: got %q want abc", got["token"])
	}

	// Default used when override blank.
	got, err = ResolveParameters(r, map[string]string{"token": "abc"})
	if err != nil {
		t.Fatalf("ResolveParameters: %v", err)
	}
	if got["model"] != "llama3" {
		t.Errorf("model default: got %q want llama3", got["model"])
	}
}

func TestParamEnvName(t *testing.T) {
	if got := ParamEnvName("hf_repo"); got != "CONTAINARIUM_PARAM_HF_REPO" {
		t.Errorf("ParamEnvName: got %q", got)
	}
}

// TestMem0Recipe locks the invariants that make the mem0 box safe to expose:
// the memory store never comes up without a Postgres password, telemetry is
// off, and the managed model-gateway (not a key in the box) drives extraction
// and embedding when the daemon brokers models.
func TestMem0Recipe(t *testing.T) {
	m := New()
	if err := m.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	r, err := m.Get("mem0")
	if err != nil {
		t.Fatalf("expected built-in recipe mem0: %v", err)
	}
	if r.RequiresGpu {
		t.Error("mem0 should not require a GPU")
