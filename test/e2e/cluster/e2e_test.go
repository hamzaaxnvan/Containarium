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

