//go:build incus && cluster_e2e

package clustere2e

// The gated e2e lane (#1418): the managed-clusters MVP journey run
// against real everything — real Incus instances, real k3s, real
// cluster-autoscaler, real VPA. Design:
// docs/architecture/managed-k8s-clusters.md, "End-to-end".
//
// One journey, two isolation classes (#1430). CONTAINARIUM_E2E_ISOLATION
// selects which: unset/vm is the VM class the self-hosted KVM lane gates,
// `container` is the Incus-system-container class (#1428/#1429) the
// GitHub-hosted lane runs without KVM. Only the `cluster create` call
// differs; every assertion below is shared, and none of them may look at
// the instance TYPE — a container-mode node is an Incus container, so a
// type filter would silently observe an empty cluster.
//
// Every assertion is against observed cluster or Incus state (the k8s
// API through the tenant kubeconfig, the Incus API, TCP reachability),
// never against the daemon's own SQL or logs. Rows are asserted through
// the product API (`cluster get` NotFound), which is the tenant-visible
// meaning of "no rows".
//
// Driven CLI-first: every platform action goes through the
// `containarium` binary the lane built, exactly as a tenant would run
// it — no agent-only escape hatches (CLAUDE.md).
//
// Steps are strictly sequential; each builds on the previous one, so a
// failed step aborts the rest instead of cascading noise.
//
// Environment contract (set by scripts/cluster-e2e.sh):
//
//	CONTAINARIUM_E2E_CLI            path to the built containarium binary
//	CONTAINARIUM_E2E_SERVER         daemon HTTP address, e.g. 127.0.0.1:8080
//	CONTAINARIUM_JWT_SECRET         daemon's JWT secret; the lane mints its tenant token
//	CONTAINARIUM_E2E_ADVERTISE_HOST host the kubeconfig must point at ("outside" reach)
//	CONTAINARIUM_E2E_SABOTAGE       "" or "join-token" (prove-the-lane-can-fail runs)
//	CONTAINARIUM_E2E_ISOLATION      "", "vm" (default) or "container" (#1430)
//	CONTAINARIUM_REQUIRE_INCUS      "1" in the lane: a missing environment fails, not skips
//
// Timeouts (durations, optional): CONTAINARIUM_E2E_READY_TIMEOUT
// (default 15m; PRD documents create→kubectl under 10 minutes),
// _SCALEUP_TIMEOUT (12m), _VPA_TIMEOUT (15m), _SCALEDOWN_TIMEOUT (30m —
// stock CA scale-down: 10m delay-after-add + 10m unneeded),
// _DELETE_TIMEOUT (10m).

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	clusterpkg "github.com/footprintai/containarium/pkg/core/cluster"
	"github.com/footprintai/containarium/pkg/core/incus"
)

const (
	tenant      = "e2etenant"
	clusterName = "lane"

	// burnImage runs both the pause-style sleepers and the CPU burner.
	// registry.k8s.io is the same registry the pinned CA image comes
	// from, and it is not rate-limited the way docker.io is.
	burnImage = "registry.k8s.io/e2e-test-images/busybox:1.36.1-1"
)

// instancePrefix is the Incus name prefix every node of the lane
// cluster carries (spec.go's naming scheme), whatever its isolation
// class: VMs in the KVM lane, system containers in the container lane.
var instancePrefix = tenant + "-k8s-" + clusterName + "-"

type lane struct {
	t             *testing.T
	cli           string
	server        string
	token         string
	advertiseHost string
	isolation     IsolationMode
	offHost       OffHostProbe
	sabotage      SabotageMode
	incus         *incus.Client

	cs  *kubernetes.Clientset
	dyn dynamic.Interface

	kubeconfig        []byte
	baselineNodes     []string
	baselineInstances []string
}

func env(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		msg := fmt.Sprintf("%s is not set; the cluster e2e needs the lane script's environment", key)
		if incusenv.DispositionFor(os.Getenv(incusenv.RequireEnv)) == incusenv.Fail {
			t.Fatalf("%s (%s is set, so this is a failure and not a skip)", msg, incusenv.RequireEnv)
		}
		t.Skipf("%s (set %s=1 to make this a failure instead)", msg, incusenv.RequireEnv)
	}
	return v
}

func timeoutEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func TestManagedClusterJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("gated KVM lane; not a -short test")
	}

	l := &lane{
		t:             t,
		cli:           env(t, "CONTAINARIUM_E2E_CLI"),
		server:        env(t, "CONTAINARIUM_E2E_SERVER"),
		advertiseHost: env(t, "CONTAINARIUM_E2E_ADVERTISE_HOST"),
		incus:         incusenv.Require(t),
	}

	secret := env(t, "CONTAINARIUM_JWT_SECRET")
	tm, err := auth.NewTokenManager(secret, "containarium")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	// The tenant's own least-privilege token — the journey is the
	// tenant's, not an admin's.
	l.token, err = tm.GenerateAccessToken(tenant, []string{"user"}, 4*time.Hour,
		auth.ScopeClustersRead, auth.ScopeClustersWrite)
	if err != nil {
		t.Fatalf("mint tenant token: %v", err)
	}

	l.offHost, err = ParseOffHostProbe(os.Getenv("CONTAINARIUM_E2E_OFFHOST_PROBE"))
	if err != nil {
		t.Fatal(err)
	}
	l.isolation, err = ParseIsolation(os.Getenv("CONTAINARIUM_E2E_ISOLATION"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Logf("isolation class under test: %s", l.isolation)

	sabotage, err := ParseSabotage(os.Getenv("CONTAINARIUM_E2E_SABOTAGE"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	l.sabotage = sabotage
	if sabotage == SabotageJoinToken {
		stop := make(chan struct{})
		defer close(stop)
		go l.sabotageJoinTokens(stop)
		t.Log("SABOTAGE ACTIVE: corrupting worker join tokens — this run is expected to go red")
	}
	if sabotage == SabotageDropPrerouting {
		t.Log("SABOTAGE ACTIVE: the published endpoint's PREROUTING DNAT rule will be deleted once the " +
			"cluster is up — the daemon host can still reach it, nothing else can. This run is expected to go red, " +
			"and a GREEN run means the off-host probe is not actually leaving the host (#1468)")
	}

	// Whatever happens, tear the cluster down so a red run does not
	// leak VMs into the next nightly. Errors are logged, not fatal —
	// the script's sweep is the backstop.
	defer func() {
		out, err := l.runCLI("cluster", "delete", clusterName)
		l.t.Logf("teardown delete: %v %s", err, strings.TrimSpace(out))
	}()

	steps := []struct {
		name string
		fn   func() error
	}{
		{"1_create_kubeconfig_from_outside", l.stepCreate},
		{"2_overflow_scales_up_via_new_ready_node", l.stepOverflowScaleUp},
		{"3_big_pod_lands_on_larger_class_node", l.stepLargerClass},
		{"4_vpa_raises_requests_without_restart_loop", l.stepVPA},
		{"5_scale_to_zero_drains_and_deletes_instance", l.stepScaleToZero},
		{"6_delete_leaves_nothing", l.stepDelete},
	}
	for _, s := range steps {
		if !t.Run(s.name, func(t *testing.T) {
			if err := s.fn(); err != nil {
				l.dumpDiagnostics()
				t.Fatal(err)
			}
		}) {
			t.Fatalf("step %s failed; aborting the journey (later steps depend on it)", s.name)
		}
	}
}

// --- CLI ----------------------------------------------------------------

// runCLI returns stdout alone on success: `cluster kubeconfig` writes
// the credential to stdout and a notice to stderr, and merging the two
// would corrupt the YAML this test parses. On error the streams are
// joined — cobra reports errors on stderr, and callers match on them.
func (l *lane) runCLI(args ...string) (string, error) {
	full := append([]string{"--http", "--server", l.server, "--token", l.token}, args...)
	cmd := exec.Command(l.cli, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), err
	}
	return stdout.String(), nil
}

// dropPrerouting deletes the published endpoint's inbound DNAT rule,
// leaving the locally-generated OUTPUT rule intact — the exact state
// that used to leave this lane green while no tenant could connect
// (#1468).
func (l *lane) dropPrerouting(hostPort string) error {
	_, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("sabotage: parse endpoint %q: %w", hostPort, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("sabotage: parse port %q: %w", portStr, err)
	}
	cmd := DropPreroutingCommand(port)
	c := exec.Command(cmd[0], cmd[1:]...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		// A rule that is not there cannot be deleted, and that is a
		// setup failure rather than the breakage under test — say so,
		// instead of letting the run go red for the wrong reason and
		// pass as "proof".
		return fmt.Errorf("sabotage: could not delete the PREROUTING rule for port %d (%v): %s\n"+
			"the proof run is invalid — it must remove a rule that exists", port, err, out.String())
	}
	l.t.Logf("SABOTAGE: deleted the PREROUTING DNAT rule for port %d; the OUTPUT rule is untouched", port)
	return nil
}

// assertReachableFromOffHost dials the advertised endpoint from a
// network namespace joined to the host by a veth, so the packet
// arrives from elsewhere and traverses PREROUTING the way a tenant's
// does (#1468).
//
// When the probe is not enabled this does NOT quietly pass. It says,
// in the run's own output, that the tenant-facing path was not
// verified — because the alternative is a step called "from outside"
// that has never left the host, which is a check that cannot fail for
// the case it is named after.
func (l *lane) assertReachableFromOffHost(hostPort string) error {
	if l.offHost != OffHostProbeNetns {
		l.t.Logf("NOT VERIFIED: the advertised endpoint %s was only dialled from the daemon host itself, "+
			"which skips PREROUTING and exercises the OUTPUT rule (#1459) instead of the DNAT rule tenants use. "+
			"Set CONTAINARIUM_E2E_OFFHOST_PROBE=netns to check it for real (#1468).", hostPort)
		return nil
	}

	// A namespace leaked by a killed run would fail setup, so clear
	// any leftovers first. Errors here are expected and ignored.
	for _, cmd := range OffHostTeardownCommands() {
		_ = exec.Command(cmd[0], cmd[1:]...).Run()
	}
	for _, cmd := range OffHostSetupCommands() {
		c := exec.Command(cmd[0], cmd[1:]...)
		var out bytes.Buffer
		c.Stdout, c.Stderr = &out, &out
		if err := c.Run(); err != nil {
			return fmt.Errorf("off-host probe setup (%s): %v\n%s", strings.Join(cmd, " "), err, out.String())
		}
	}
	defer func() {
		for _, cmd := range OffHostTeardownCommands() {
			_ = exec.Command(cmd[0], cmd[1:]...).Run()
		}
	}()

	dial := OffHostDialCommand(hostPort, 15*time.Second)
	c := exec.Command(dial[0], dial[1:]...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	code := strings.TrimSpace(stdout.String())

	// Any HTTP status proves the packet reached the API server through
	// PREROUTING. 401/403 is a pass: authentication is not what is
	// under test, reachability is. Only a failure to connect fails.
	if err != nil || code == "" || code == "000" {
		return fmt.Errorf(
			"the advertised endpoint %s is NOT reachable from off-host (curl=%v code=%q): %s\n"+
				"the kubeconfig names an address a tenant cannot reach. This is the PREROUTING/DNAT path, "+
				"which the daemon host itself never traverses (#1468)",
			hostPort, err, code, strings.TrimSpace(stderr.String()))
	}
	l.t.Logf("reachable from off-host: %s answered HTTP %s from netns %s (traversed PREROUTING)",
		hostPort, code, OffHostNetns)
	return nil
}

// --- step 1: create → kubeconfig works from outside ---------------------

func (l *lane) stepCreate() error {
	readyTimeout := timeoutEnv("CONTAINARIUM_E2E_READY_TIMEOUT", 15*time.Minute)

	// CLI-first, and the isolation class is a CLI flag exactly as a
	// tenant would pass it (#1428). VM mode adds no flag at all.
	createArgs := append([]string{"cluster", "create", clusterName}, l.isolation.CreateArgs()...)
	if out, err := l.runCLI(createArgs...); err != nil {
		return fmt.Errorf("cluster create (%s): %v\n%s", l.isolation, err, out)
	}

	// The kubeconfig RPC is FailedPrecondition until the cluster is
	// READY, then returns the credential: polling it IS the readiness
	// probe, through the same door a tenant uses.
	err := l.waitFor("cluster READY and kubeconfig served", readyTimeout, 10*time.Second, func() (bool, string) {
		out, err := l.runCLI("cluster", "kubeconfig", clusterName)
		if err != nil {
			return false, strings.TrimSpace(out)
		}
		l.kubeconfig = []byte(out)
		return true, "kubeconfig fetched"
	})
	if err != nil {
		return err
	}

	// "From outside": the credential must point at the advertised host
	// (the passthrough route), not at a VM address only the host can
	// reach.
	host, err := KubeconfigServerHost(l.kubeconfig)
	if err != nil {
		return err
	}
	if got, _, splitErr := net.SplitHostPort(host); splitErr != nil || got != l.advertiseHost {
		return fmt.Errorf("kubeconfig points at %q, want the advertised host %q — that is not the address a tenant is given", host, l.advertiseHost)
	}

	// ...and that address must actually answer from OFF the host
	// (#1468). Everything above, and every l.cs call below, runs on
	// the daemon host: those packets are locally generated, skip
	// PREROUTING, and take the OUTPUT path that #1459 added for the
	// lane's own benefit. The DNAT rule every real tenant traverses is
	// in PREROUTING, and nothing here had ever exercised it — delete
	// that rule and this suite stayed green while no tenant could
	// reach any cluster.
	if l.sabotage == SabotageDropPrerouting {
		if err := l.dropPrerouting(host); err != nil {
			return err
		}
	}
	if err := l.assertReachableFromOffHost(host); err != nil {
		return err
	}

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(l.kubeconfig)
	if err != nil {
		return fmt.Errorf("kubeconfig rest config: %w", err)
	}
	if l.cs, err = kubernetes.NewForConfig(restCfg); err != nil {
		return fmt.Errorf("clientset: %w", err)
	}
	if l.dyn, err = dynamic.NewForConfig(restCfg); err != nil {
