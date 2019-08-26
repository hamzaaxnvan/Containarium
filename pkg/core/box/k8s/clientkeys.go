package k8s

import (
	"context"
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Client (agent) keys are recorded on the per-tenant Secret under this field,
// SEPARATE from authorized_keys (which is what the box's dropbear reads and,
// in gateway-upstream mode, holds the gateway key instead). The sentinel's
// keysync consumes these via ListClientKeys → /authorized-keys so the
// sentinel authenticates the AGENT at hop 1 — deriving them from the Pipe's
// from.authorized_keys_data would be wrong once the sentinel's own key is
// appended there.
const clientKeysKey = "client_keys"

// ClientKeyLister is the seam the daemon's /authorized-keys handler consumes
// on the k8s runtime (the LXC runtime walks /home instead).
type ClientKeyLister interface {
	ListClientKeys(ctx context.Context) (map[string]string, error)
}

var _ ClientKeyLister = (*Backend)(nil)

// ListClientKeys returns tenant → client authorized_keys content for every
// box this backend manages, read from the client_keys field of each
// per-tenant Secret. Boxes created before the field existed are skipped with
// a log line (their keys re-appear on the next SetAuthorizedKeys).
func (b *Backend) ListClientKeys(ctx context.Context) (map[string]string, error) {
	boxes, err := b.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("k8s: list boxes for client keys: %w", err)
	}
	out := make(map[string]string, len(boxes))
	for i := range boxes {
		tenant := boxes[i].Ref.Tenant
		sec, gerr := b.clientset.CoreV1().Secrets(b.namespaceFor(tenant)).Get(ctx, secretName(tenant), metav1.GetOptions{})
		if gerr != nil {
			log.Printf("[k8s] client keys for %s unavailable: %v (skipping)", tenant, gerr)
			continue
		}
		keys := string(sec.Data[clientKeysKey])
		if keys == "" {
			log.Printf("[k8s] box %s has no recorded client_keys (created pre-advertisement?); skipping until next key rotation", tenant)
			continue
		}
