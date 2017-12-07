package traffic

import (
	"sort"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// EgressStat is one container's egress fan-out for a snapshot window — the
// crawler-detection signal (docs/EGRESS-FANOUT-DETECTION.md). DistinctDestinations
// is the number of unique destination IPs the container connected out to in the
// window; a crawler's hallmark is a large, churning fan-out where a normal app
// talks to a handful of destinations. EgressConnections is the total egress
// connection count. ContainerID is the cloud_container_id label (empty on
// non-cloud boxes), the tenant join key shared with the bytes plane.
//
// Deliberately a per-container *aggregate*: we count the fan-out, we do not turn
// each destination IP into its own series. Per-destination series would be
// unbounded cardinality and would index every site a customer's box visits —
