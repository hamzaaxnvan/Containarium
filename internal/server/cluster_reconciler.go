//go:build !containarium_client

package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	clusterstore "github.com/footprintai/containarium/internal/cluster"
	clustercore "github.com/footprintai/containarium/pkg/core/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ClusterReconciler converges managed-cluster records (#1414): it
// reads desired state from the store, observes VMs on the host, and
// executes whatever the pure clustercore.Decide returns. All policy
// lives in Decide; this loop is plumbing plus bookkeeping (node rows,
// scale events, state transitions).
//
// One pass is idempotent and crash-safe: nothing here holds state
// between ticks, so a daemon restart resumes wherever the store and
// the host actually are.
type ClusterReconciler struct {
	store clusterstore.Store
	mgr   *clustercore.Manager

	// admit is the CPU-admission seam (nil = no gate, unit tests).
	// Refusals are recorded as events, never silent. The returned release
	// func (#1588) must be called once this reconciler's own provisioning
	// attempt concludes — see admitSize.
	admit func(owner, cpu string) (release func(), err error)

	// publish allocates/records the external API endpoint for a
	// cluster whose control plane is up; unpublish reverses it.
	// Seamed for tests; production wires the passthrough route.
	publish   func(ctx context.Context, c *clusterstore.Cluster, cpIP string) (string, error)
	unpublish func(ctx context.Context, c *clusterstore.Cluster) error

	// advertiseHost is the host part of the address publish hands out,
	// recorded by WireEndpointPublisher. It must be a subject-alt-name
	// on the control plane's certificate, or the kubeconfig we give the
	// tenant cannot verify against the endpoint it dials (#1464).
	advertiseHost string

	// vpaDisabled turns off VPA deployment (CONTAINARIUM_CLUSTER_VPA=false);
	// default is on — pod vertical autoscaling is a P0 story (#1416).
	vpaDisabled bool

	// caAddr + mintCACert enable autoscaler deployment onto each
	// cluster's control plane (#1415, mTLS transport). Nil mint =
	// clusters run without an autoscaler (reconciler still converges
	// to min).
	caAddr     string
	mintCACert func(owner, name string) (clustercore.CACredentials, error)

	// caScaleDownDelay / caScaleDownUnneeded override the autoscaler's
	// scale-down timers; zero = stock. See SetCAScaleDown.
	caScaleDownDelay    time.Duration
	caScaleDownUnneeded time.Duration

	interval time.Duration
}

// NewClusterReconciler wires the loop. publish/unpublish may be nil
// (endpoint stays the CP IP; nothing to reverse).
func NewClusterReconciler(store clusterstore.Store, mgr *clustercore.Manager) *ClusterReconciler {
	return &ClusterReconciler{store: store, mgr: mgr, interval: 15 * time.Second}
}

// SetAdmission wires the CPU-admission gate.
func (r *ClusterReconciler) SetAdmission(f func(owner, cpu string) (release func(), err error)) {
	r.admit = f
}

// SetVPADisabled turns off VPA deployment into new clusters.
func (r *ClusterReconciler) SetVPADisabled(v bool) { r.vpaDisabled = v }

// SetCADeployer wires autoscaler deployment: providerAddr is the
// daemon's CA-provider mTLS listener as reachable from cluster VMs,
// mint issues each cluster's client credential.
func (r *ClusterReconciler) SetCADeployer(providerAddr string, mint func(owner, name string) (clustercore.CACredentials, error)) {
	r.caAddr, r.mintCACert = providerAddr, mint
}

// SetCAScaleDown overrides the autoscaler's scale-down-delay-after-add and
// scale-down-unneeded-time for clusters deployed from now on. Zero keeps the
// stock 10m. Meant for test lanes (CONTAINARIUM_CLUSTER_CA_SCALE_DOWN_*),
// where the stock ~20m between a scale-up and the surplus node's removal is
// pure waiting.
func (r *ClusterReconciler) SetCAScaleDown(delayAfterAdd, unneededTime time.Duration) {
	r.caScaleDownDelay, r.caScaleDownUnneeded = delayAfterAdd, unneededTime
}

// parseCAScaleDownEnv reads CONTAINARIUM_CLUSTER_CA_SCALE_DOWN_DELAY_AFTER_ADD
// and CONTAINARIUM_CLUSTER_CA_SCALE_DOWN_UNNEEDED_TIME as Go durations. Unset
// means zero (stock timers); a set value must be a positive duration, so a
// typo fails loudly instead of silently running with defaults.
func parseCAScaleDownEnv(getenv func(string) string) (delayAfterAdd, unneededTime time.Duration, err error) {
	parse := func(key string) (time.Duration, error) {
		v := getenv(key)
		if v == "" {
			return 0, nil
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("%s=%q: %w", key, v, err)
		}
		if d <= 0 {
			return 0, fmt.Errorf("%s=%q: must be a positive duration (unset it for the stock timer)", key, v)
		}
		return d, nil
	}
	if delayAfterAdd, err = parse("CONTAINARIUM_CLUSTER_CA_SCALE_DOWN_DELAY_AFTER_ADD"); err != nil {
		return 0, 0, err
	}
	if unneededTime, err = parse("CONTAINARIUM_CLUSTER_CA_SCALE_DOWN_UNNEEDED_TIME"); err != nil {
		return 0, 0, err
	}
	return delayAfterAdd, unneededTime, nil
}

// SetEndpointPublisher wires endpoint publish/unpublish.
func (r *ClusterReconciler) SetEndpointPublisher(
	publish func(ctx context.Context, c *clusterstore.Cluster, cpIP string) (string, error),
	unpublish func(ctx context.Context, c *clusterstore.Cluster) error,
) {
	r.publish, r.unpublish = publish, unpublish
}

// ReadKubeconfig implements the ClusterServer's KubeconfigReader seam:
// read on demand from the CP VM, rewritten to the published endpoint,
// never persisted.
func (r *ClusterReconciler) ReadKubeconfig(ctx context.Context, c *clusterstore.Cluster) (string, error) {
	return r.mgr.Kubeconfig(c.Owner, c.Name, c.APIEndpoint)
}

// NodeCapable is the create-time capability probe for one isolation
// class (#1429): the seam carries the class and the probe dispatches
// on it — a VM cluster needs VM capability, a container cluster needs
// the container-node preconditions, never both.
func (r *ClusterReconciler) NodeCapable(iso clusterstore.Isolation) error {
	if coreIsolation(iso) == clustercore.IsolationContainer {
		return r.mgr.ContainerNodeCapable()
	}
	return r.mgr.VMCapable()
}

// coreIsolation maps the stored class onto the provisioning seam's.
// Anything that is not the explicit weaker class provisions VMs.
func coreIsolation(i clusterstore.Isolation) clustercore.Isolation {
	if i.OrDefault() == clusterstore.IsolationContainer {
		return clustercore.IsolationContainer
	}
	return clustercore.IsolationVM
}

// Run ticks until ctx ends.
func (r *ClusterReconciler) Run(ctx context.Context) {
	log.Printf("[cluster] reconciler running (interval %v)", r.interval)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.ReconcileOnce(ctx)
		}
	}
}

// ReconcileOnce runs one pass over every cluster.
func (r *ClusterReconciler) ReconcileOnce(ctx context.Context) {
	clusters, err := r.store.List(ctx, "")
	if err != nil {
		log.Printf("[cluster] reconcile: list: %v", err)
		return
