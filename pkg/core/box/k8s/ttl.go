package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/footprintai/containarium/pkg/core/box"
)

// metaTTLExpiresAtKey is the meta key (under metaAnnotationPrefix) mirroring
// the Sandbox's spec.shutdownTime. The daemon's TTL sweeper reads box meta
// (BoxStatus.Labels, fed from these annotations) — it never parses the CRD —
// so the expiry is mirrored here in the same RFC3339 form the sweeper already
// understands from the Incus path.
const metaTTLExpiresAtKey = "ttl_expires_at"

var _ box.TTLCapable = (*Backend)(nil)

// SetTTL stamps (or clears, expiresAt == nil) the box's absolute auto-delete
// time.
