// Package controller holds the Box CRD reconciler — the operator half of the
// Kubernetes agent-box control plane (#995). It reconciles a containarium.dev
// Box into the per-tenant bundle by delegating to a box.BoxBackend, the same
// Create/Delete the imperative API uses, so both paths converge on one builder.
package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	containariumv1alpha1 "github.com/footprintai/containarium/apis/containarium/v1alpha1"
	box "github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// boxFinalizer makes the reconciler tear the box down via the backend before
// the Box CR is removed from the API server.
const boxFinalizer = "containarium.dev/box"

// requeueInterval refreshes status while a box is still coming up.
const requeueInterval = 30 * time.Second

// BoxReconciler reconciles a Box into the per-tenant agent-box bundle
// (Sandbox + Pipe + Secret + NetworkPolicy) by delegating to a box.BoxBackend.
// It owns only the Box CR's finalizer and status; the backend owns the
// downstream objects.
type BoxReconciler struct {
	client.Client
	Backend box.BoxBackend
}

// +kubebuilder:rbac:groups=containarium.dev,resources=boxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=containarium.dev,resources=boxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=containarium.dev,resources=boxes/finalizers,verbs=update

// Reconcile drives the cluster toward the Box's spec.
func (r *BoxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var b containariumv1alpha1.Box
	if err := r.Get(ctx, req.NamespacedName, &b); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ref := box.BoxRef{Tenant: tenantOf(&b)}

	// Deletion: tear down via the backend, then drop the finalizer.
	if !b.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&b, boxFinalizer) {
			if err := r.Backend.Delete(ctx, ref, false); err != nil {
				return ctrl.Result{}, fmt.Errorf("delete box %q: %w", ref.Tenant, err)
			}
			controllerutil.RemoveFinalizer(&b, boxFinalizer)
			if err := r.Update(ctx, &b); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the finalizer before creating anything the backend must clean up.
	if controllerutil.AddFinalizer(&b, boxFinalizer) {
		if err := r.Update(ctx, &b); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Create/ensure the box (idempotent) — the single convergent builder.
	st, err := r.Backend.Create(ctx, specFromBox(&b))
	if err != nil {
		b.Status.Phase = containariumv1alpha1.BoxFailed
		meta.SetStatusCondition(&b.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "CreateFailed",
			Message:            err.Error(),
			ObservedGeneration: b.Generation,
		})
		_ = r.Status().Update(ctx, &b)
		return ctrl.Result{}, fmt.Errorf("ensure box %q: %w", ref.Tenant, err)
	}

	r.applyStatus(ctx, &b, st)
	if err := r.Status().Update(ctx, &b); err != nil {
		return ctrl.Result{}, err
	}
	// Re-check while the pod may still be starting.
