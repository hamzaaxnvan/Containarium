package cloudexport

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// DefaultIntervalSeconds is the fixed export cadence for the MVP. #1069
// does not expose an operator knob to change it — see the "cost guard"
// floor in docs/CLOUD-NATIVE-METRICS-EXPORT-DESIGN.md (custom metrics
// are billed per ingested sample).
const DefaultIntervalSeconds = 60

