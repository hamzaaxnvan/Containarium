package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	containariumv1alpha1 "github.com/footprintai/containarium/apis/containarium/v1alpha1"
	box "github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBackend is a minimal box.BoxBackend that records Create/Delete and
// reports a running box, so reconcile logic can be tested without a cluster.
type fakeBackend struct {
	created   []box.BoxSpec
	deleted   []string
	createErr error
}

func (f *fakeBackend) Kind() box.BackendKind { return "" }
func (f *fakeBackend) Create(_ context.Context, spec box.BoxSpec) (*box.BoxStatus, error) {
	if f.createErr != nil {
