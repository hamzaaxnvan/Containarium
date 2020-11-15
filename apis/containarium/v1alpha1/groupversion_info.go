// Package v1alpha1 contains the API types for the containarium.dev group — the
// Box CRD the daemon reconciles into a per-tenant agent-box (Sandbox + Pipe +
// Secret + NetworkPolicy). See issue #995 for the operator design.
//
// +kubebuilder:object:generate=true
// +groupName=containarium.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
