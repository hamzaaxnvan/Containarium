package audit

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/footprintai/containarium/pkg/core/box/k8s/boxmeta"
)

// The K8s session source (#1189).
//
// A K8s box has no /var/log/auth.log to grep: it runs dropbear as PID 1's
// child with logging to stderr, which the kubelet captures. So the session
// record lives in the pod log, and reading it is an apiserver call rather
// than an exec.
//
// This is why the collector needed a source seam at all. The LXC path
// (exec + grep + sshd patterns) and this one (pod log + dropbear patterns)
// share no step; what they share is everything the collector does with the
// result.

// k8sLogLookback bounds the first read of a box the collector has not seen
// before — on startup, or when a box first appears.
//
// Not unbounded: a box that has been up for months would have the collector
// pull its entire retained log on the first pass, and every record in it is
// one the previous run already wrote. Not short either, because anything
// older than the window on that first pass is lost rather than late. A day
// covers a daemon restart and any plausible collection gap.
const k8sLogLookback = 24 * time.Hour

// k8sSessionSource reads dropbear's output from box pod logs.
type k8sSessionSource struct {
	clientset kubernetes.Interface
	// namespace scopes the pod listing; empty means every namespace, which
	// is the normal case since boxes get a namespace per tenant.
	namespace string

	// mu guards lastRead, which is consulted from the collector's goroutine
	// only today but is cheap to make safe.
	mu sync.Mutex
	// lastRead is the point each box's log was last read up to, used as the
	// apiserver's SinceTime so a poll transfers a poll's worth of log rather
	// than the whole retained history.
	//
	// This is an efficiency bound, NOT de-duplication: the window is
	// deliberately re-read with an overlap and the collector's high-water
	// mark drops what it has already recorded. Using it to skip lines would
	// mean a clock skew between the node and the daemon silently losing
	// logins.
	lastRead map[string]time.Time
}

// NewK8sSessionSource returns the pod-log session source.
//
// namespace scopes the search; pass "" to cover every namespace.
func NewK8sSessionSource(clientset kubernetes.Interface, namespace string) SessionSource {
	return &k8sSessionSource{
		clientset: clientset,
		namespace: namespace,
		lastRead:  make(map[string]time.Time),
	}
}

func (s *k8sSessionSource) Boxes(ctx context.Context) ([]SessionBox, error) {
	pods, err := s.clientset.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: boxmeta.ManagedByLabel + "=" + boxmeta.ManagedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("list box pods: %w", err)
	}

	var boxes []SessionBox
	for i := range pods.Items {
		pod := &pods.Items[i]
		tenant := pod.Labels[boxmeta.TenantLabel]
		if tenant == "" {
			// Managed by this platform but not a tenant box — the gateway,
			// for instance. Nobody logs into those as a tenant.
			continue
		}
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		boxes = append(boxes, SessionBox{
			// The tenant, not the pod name: pods are replaced on restart and
			// resize, and an audit trail whose resource ID changes underneath
			// it cannot be queried for "who logged into this box".
			Name:     tenant,
			Username: tenant,
			Ref:      pod.Namespace + "/" + pod.Name,
		})
	}

	// Drop windows for pods that no longer exist. The key is the pod, and a
	// pod is replaced on every restart and resize — so without this the map
	// gains an entry per box lifetime and never loses one, in a process meant
	// to run for months.
	//
	// Only on a successful list: pruning on a transient empty result would
	// reset every window to the initial lookback, making the next pass re-read
	// a day of logs for the whole fleet.
	s.retainWindows(boxes)

	return boxes, nil
}

// retainWindows forgets the read window of every box not in the current list.
func (s *k8sSessionSource) retainWindows(boxes []SessionBox) {
	live := make(map[string]struct{}, len(boxes))
	for _, b := range boxes {
		live[b.Ref] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
