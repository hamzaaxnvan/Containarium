// Package boxmeta holds the labels and names that identify a K8s box's
// Kubernetes objects.
//
// It is a leaf package on purpose. The daemon creates box pods (pkg/core/box/k8s)
// and the SSH session collector finds them again (internal/audit), and those two
