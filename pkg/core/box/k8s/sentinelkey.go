package k8s

import (
	"context"
	"fmt"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/footprintai/containarium/internal/gateway"
)

// The sentinel's upstream pubkey(s) are stored in this Secret in the gateway
// namespace (restart-safe) and appended to every box Pipe's from-keys so a
// sentinel fronting this node can complete hop 2 of the SSH chain
// (agent → sentinel → node gateway → box). Distinct from the box's own
// authorized_keys and the daemon's gateway-upstream key.
const (
	sentinelKeySecretName = "containarium-sentinel-keys"
	sentinelKeysField     = "authorized_keys"
)

var _ gateway.SentinelKeyAuthorizer = (*Backend)(nil)

// SetSentinelKey persists the sentinel's upstream pubkey and authorizes it at
