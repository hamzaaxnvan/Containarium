package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	containariumv1alpha1 "github.com/footprintai/containarium/apis/containarium/v1alpha1"
	box "github.com/footprintai/containarium/pkg/core/box"
)

// BoxWriter upserts a Box CR from a runtime-neutral box.BoxSpec. It is the
// convergence primitive (#995, slice 4): when the operator is enabled, the
// imperative create path writes a Box CR through this instead of calling the
// backend directly, so the reconciler is the single builder for both the
// imperative and the declarative (kubectl apply / GitOps) paths.
type BoxWriter struct {
	client    ctrlclient.Client
	namespace string
}

// NewBoxWriter builds a BoxWriter with a direct (uncached) client. namespace is
// where Box CRs are created.
func NewBoxWriter(namespace string) (*BoxWriter, error) {
	cfg, err := ctrlconfig.GetConfig()
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	if err := containariumv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
