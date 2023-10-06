package cluster

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHost records every VMHost call so tests assert the exact
// orchestration sequence — the part of the manager that matters.
type fakeHost struct {
	calls             []string
	files             map[string][]byte // "<vm>:<path>" → content
	execOut           map[string]string // "<vm>:<argv0...>" → stdout
	readErr           error
	capError          error
	containerCapError error
	capacityError     error
	// isolations records the isolation class CreateNode was called
	// with, per node name (#1429 acceptance: the fake records the
	// isolation per call).
	isolations map[string]Isolation
	stopErr    error
	deleteErr  error
	execErr    error
	// deleteFailTimes, when > 0, makes Delete return deleteErr for that
	// many calls before succeeding — simulates Incus's transient
	// "Instance is running" race (#1882): Stop's own operation
	// completing does not guarantee the instance's queryable state has
	// settled to Stopped by the time the very next API call runs.
	deleteFailTimes int
	// deleted records every instance Delete removed, so a test can
	// assert the removal happened without reading it back out of the
	// call log.
	deleted map[string]bool
}

func newFakeHost() *fakeHost {
	return &fakeHost{files: map[string][]byte{}, execOut: map[string]string{}, isolations: map[string]Isolation{},
		deleted: map[string]bool{}}
}

func (f *fakeHost) record(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

func (f *fakeHost) VMCapable() error            { return f.capError }
func (f *fakeHost) ContainerNodeCapable() error { return f.containerCapError }

func (f *fakeHost) NodeCapacityCapable(NodeSpec) error { return f.capacityError }
func (f *fakeHost) CreateNode(spec NodeSpec, isolation Isolation) error {
	f.record("create %s cpu=%s mem=%s disk=%s role=%s", spec.Name, spec.CPU, spec.Memory, spec.Disk, spec.Labels[LabelClusterRole])
	f.isolations[spec.Name] = isolation
	return nil
}
func (f *fakeHost) Start(name string) error { f.record("start %s", name); return nil }
func (f *fakeHost) Stop(name string) error  { f.record("stop %s", name); return f.stopErr }
func (f *fakeHost) Delete(name string) error {
	f.record("delete %s", name)
	if f.deleteFailTimes > 0 {
		f.deleteFailTimes--
		err := f.deleteErr
		if f.deleteFailTimes == 0 {
			// The transient window is over — later calls (including
			// this fake's own persistent-failure mode, which never sets
			// deleteFailTimes) fall through to the plain check below.
			f.deleteErr = nil
		}
		return err
	}
	if f.deleteErr == nil {
		f.deleted[name] = true
	}
	return f.deleteErr
}
func (f *fakeHost) WaitReady(name string, _ time.Duration) (string, error) {
	f.record("wait %s", name)
	return "10.166.11.5", nil
}
func (f *fakeHost) Push(name, path string, content []byte, mode string) error {
	f.record("push %s:%s mode=%s", name, path, mode)
	f.files[name+":"+path] = content
	return nil
}
func (f *fakeHost) Read(name, path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	f.record("read %s:%s", name, path)
	if c, ok := f.files[name+":"+path]; ok {
		return c, nil
	}
	return []byte("stored-content"), nil
}
func (f *fakeHost) Exec(name string, cmd []string) (string, error) {
	f.record("exec %s:%s", name, strings.Join(cmd, " "))
	if f.execErr != nil {
		return "", f.execErr
	}
	// Keyed on the whole argv first so a test can stub two commands
	// that share an argv0 (cat /proc/cpuinfo vs cat /proc/meminfo);
	// argv0 alone remains for the stubs that predate that need.
	if out, ok := f.execOut[name+":"+strings.Join(cmd, " ")]; ok {
		return out, nil
	}
	return f.execOut[name+":"+cmd[0]], nil
}

// setNodeProc stubs what a node reports about itself once booted —
// the vantage point a reservation may be derived from, and the only
// one that can tell a nested host's lie from a plain host's truth
// (#1466). Explicit in every test that needs it: a fake that served a
// plausible default /proc would hide exactly the bug under test.
func (f *fakeHost) setNodeProc(name string, cpu int, memKB int64) {
	var b strings.Builder
	for i := 0; i < cpu; i++ {
		fmt.Fprintf(&b, "processor\t: %d\nvendor_id\t: GenuineIntel\n\n", i)
	}
	f.execOut[name+":cat "+ProcCPUInfoPath] = b.String()
	f.execOut[name+":cat "+ProcMemInfoPath] = fmt.Sprintf("MemTotal:%15d kB\nMemFree:  1024 kB\n", memKB)
}
func (f *fakeHost) ClusterVMs(tenant, clusterName string) (Observed, error) {
	return Observed{}, nil
}

func testManager(f *fakeHost) *Manager {
	return &Manager{
		host:             f,
		k3sBinary:        func() ([]byte, error) { return []byte("k3s-binary-bytes"), nil },
		waitReadyTimeout: time.Second,
	}
}

func TestProvisionCPSequence(t *testing.T) {
	f := newFakeHost()
	m := testManager(f)

	ip, err := m.ProvisionCP("alice", "demo", IsolationVM, DesiredGroup{CPU: "2", Memory: "4GB", Disk: "40GB"}, []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("ProvisionCP: %v", err)
	}
	if ip != "10.166.11.5" {
		t.Fatalf("ip = %q", ip)
	}
	want := []string{
		"create alice-k8s-demo-cp cpu=2 mem=4GB disk=40GB role=control-plane",
		"wait alice-k8s-demo-cp",
		// Each push is preceded by its parent mkdir (#1442): Incus
		// answers Not Found for a missing parent, and these
		// directories do not exist in a fresh node image.
		"exec alice-k8s-demo-cp:mkdir -p " + filepath.Dir(K3sBinaryPath),
		"push alice-k8s-demo-cp:" + K3sBinaryPath + " mode=0755",
		"exec alice-k8s-demo-cp:mkdir -p " + filepath.Dir(bootstrapScriptPath),
		"push alice-k8s-demo-cp:" + bootstrapScriptPath + " mode=0755",
		"exec alice-k8s-demo-cp:sh " + bootstrapScriptPath,
	}
	assertCalls(t, f.calls, want)

	// The pushed script carries BOTH the VM IP and the external SAN.
	script := string(f.files["alice-k8s-demo-cp:"+bootstrapScriptPath])
	for _, san := range []string{"--tls-san 10.166.11.5", "--tls-san 203.0.113.10"} {
		if !strings.Contains(script, san) {
			t.Fatalf("server script missing %q", san)
		}
	}
}

func TestProvisionWorkerSequence(t *testing.T) {
	f := newFakeHost()
	// Shaped like a real k3s node-token — CA hash, separator,
	// credential, trailing newline — so byte-identity below means
	// the CA hash and the newline both survived the round trip
	// (#1446). Value is synthetic.
	const nodeToken = "K10aaaabbbbccccddddeeeeffff00001111222233334444555566667777888899::server:0123456789abcdef\n"
	f.files["alice-k8s-demo-cp:"+NodeTokenPath] = []byte(nodeToken)
	m := testManager(f)

	err := m.ProvisionWorker("alice", "demo", IsolationVM,
		DesiredGroup{Name: "small", CPU: "2", Memory: "4GB", Disk: "40GB"},
		"alice-k8s-demo-small-1", "10.166.11.5")
	if err != nil {
		t.Fatalf("ProvisionWorker: %v", err)
	}
	want := []string{
		"read alice-k8s-demo-cp:" + NodeTokenPath,
		"create alice-k8s-demo-small-1 cpu=2 mem=4GB disk=40GB role=worker",
		"wait alice-k8s-demo-small-1",
		"exec alice-k8s-demo-small-1:mkdir -p " + filepath.Dir(K3sBinaryPath),
		"push alice-k8s-demo-small-1:" + K3sBinaryPath + " mode=0755",
		// The one that was actually failing in production: /etc/containarium
		// does not exist in a fresh image, so this push returned Not Found.
		"exec alice-k8s-demo-small-1:mkdir -p " + filepath.Dir(AgentTokenPath),
		"push alice-k8s-demo-small-1:" + AgentTokenPath + " mode=0600",
		"exec alice-k8s-demo-small-1:mkdir -p " + filepath.Dir(bootstrapScriptPath),
		"push alice-k8s-demo-small-1:" + bootstrapScriptPath + " mode=0755",
