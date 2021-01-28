//go:build !containarium_client

package server

// VPA deployment through the reconciler (#1416): the pinned bundle and
// a per-cluster webhook secret land on the CP via the k3s auto-apply
// dir, exactly once.

import (
	"context"
	"strings"
	"testing"

	clustercore "github.com/footprintai/containarium/pkg/core/cluster"
)

func TestReconciler_DeploysVPAOntoCP(t *testing.T) {
	srv, rec, host := testReconcilerRig(t)
	mustCreate(t, srv, tenantCtx("alice"), "demo")
	ctx := context.Background()
	rec.ReconcileOnce(ctx)
	rec.ReconcileOnce(ctx)
	rec.ReconcileOnce(ctx)

	cp := "alice-k8s-demo-cp"
	manifests := string(host.files[cp+":"+clustercore.VPAManifestPath])
	if !strings.Contains(manifests, "vpa-recommender") || !strings.Contains(manifests, "@sha256:") {
		t.Fatal("digest-pinned VPA manifests not deployed to the CP")
	}
	secretA := string(host.files[cp+":"+clustercore.VPACertsPath])
	if !strings.Contains(secretA, "vpa-tls-certs") {
		t.Fatal("VPA webhook secret not deployed")
	}
	// Further passes never rotate the webhook secret.
	rec.ReconcileOnce(ctx)
	if string(host.files[cp+":"+clustercore.VPACertsPath]) != secretA {
		t.Fatal("reconciler pass rotated the deployed VPA webhook secret")
	}

