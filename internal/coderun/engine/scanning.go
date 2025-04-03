package engine

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ModelTrafficScanning says whether a run's model responses are scanned by
// the inbound guardrail (#2367). A tenant-secret run talks to the provider
// directly, so its traffic never crosses the gateway and is never scanned.
// A gateway run follows effective, the daemon's own answer to `guardrail
// status` (#2737): ADMIN is SCANNED, BASELINE (no policy ever set) is the
