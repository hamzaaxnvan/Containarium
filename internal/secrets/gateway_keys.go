package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// Per-owner model-gateway provider keys.
//
// The daemon's model gateway can broker calls for several key owners, each
// spending their own upstream key (internal/modelgateway, `key_owner` claim).
// Those keys need the same custody as any tenant secret — encrypted at rest,
// KMS-enveloped in production — so they live in THIS store rather than in a
// second one, under a reserved namespace:
//
//	__gateway/<key_owner>/<provider>
//
// which maps onto the store's (username, name) pair as
// username="__gateway/<key_owner>", name="<PROVIDER>". The split is forced by
// the store's own name rule (env-var-shaped, `^[A-Z_][A-Z0-9_]*$`), which a
// single literal path could never satisfy.
//
// Two properties this namespace has to have, and how it gets them:
//
//   - No tenant can ever list, read, overwrite or delete one. A '/' cannot
//     appear in a tenant username, so the reserved namespace is disjoint from
//     every tenant's by construction rather than by a filter someone has to
//     remember. On top of that, the tenant-facing methods refuse the namespace
//     outright (ErrReservedNamespace), so an RPC that forwards a
//     caller-supplied username cannot reach it either.
//   - No delivery mode ever ships one into a box. The rows are stored with
//     DeliveryBroker, which LoadAllForUserWithDelivery — the single funnel every
//     delivery path goes through — already excludes.

// gatewayKeyNamespacePrefix prefixes the reserved username of every per-owner
// gateway key. The '/' is what makes it unreachable as a tenant username.
