package controller

import (
	"context"
	"testing"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	containariumv1alpha1 "github.com/footprintai/containarium/apis/containarium/v1alpha1"
	box "github.com/footprintai/containarium/pkg/core/box"
)

// TestUpsertBox: the convergent create path writes a Box CR from a BoxSpec, and
