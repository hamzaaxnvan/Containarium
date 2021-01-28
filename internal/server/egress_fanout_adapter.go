package server

import (
	metricsPackage "github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/traffic"
)

// EgressFanoutFetcherAdapter adapts the conntrack traffic collector to the
// metrics.EgressFanoutFetcher interface, bridging traffic.EgressStat to the
// metrics-boundary type so neither package imports the other. Supplies the
// egress fan-out crawler-detection signal to the OTel metrics collector.
