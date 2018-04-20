package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Delivering tenant secrets to a K8s box (#1190).
//
// On LXC a secret is delivered by `incus config set environment.<NAME>` (env
// mode) or by writing /run/secrets/<NAME> into the container's tmpfs (file
// mode). Neither has a K8s equivalent, so `containarium secret set` succeeded
// and the value never reached the box — the API call looks identical on both
// backends.
//
// The delivery here is a per-tenant Secret mounted into the box, and the
// choice of a mount over env vars is forced rather than stylistic:
//
//   - A container's environment is fixed when it starts. Updating a Secret
//     that was projected with envFrom does NOT change a running pod; the pod
//     has to be recreated. That fails the requirement that RefreshSecrets
//     update a running box.
//   - A Secret *volume* is refreshed in place by the kubelet, so a new
//     session sees the new value with no restart. That is also what the LXC
//     path actually promises — its own RefreshSecrets message says "new execs
//     will see updated values", not that running processes are re-parented.
//
// So the mount is what makes the two backends agree, and env projection is
// what would have made them differ while looking correct.
//
// The mount is tmpfs (Kubernetes backs Secret volumes with memory), which
// preserves the ephemeral-disposal property file-mode secrets have on LXC:
// when the pod goes, the plaintext goes with it. Nothing is written to the
// box's PVC or into the Sandbox CR — the CR carries the Secret's name, never
// its contents.

const (
	// tenantSecretsVolume is the pod volume carrying the tenant's secrets.
	tenantSecretsVolume = "tenant-secrets"
	// TenantSecretsMount is where the box sees them, one file per secret.
	// Same path as the LXC file-delivery mode, so anything reading secrets
	// from disk works unchanged on both backends.
	TenantSecretsMount = "/run/secrets"
)

