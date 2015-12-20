package containariumotel

import "time"

// Option configures Init. Pass options to Init like:
//
//	containariumotel.Init(ctx,
//	    containariumotel.WithServiceName("payment-api"),
//	    containariumotel.WithMetricInterval(10*time.Second),
//	)
type Option func(*options)

type options struct {
	serviceName    string
	endpointURL    string
	extraAttrs     map[string]string
	metricInterval time.Duration
	metricTimeout  time.Duration
}

// defaultOptions match the central collector / sidecar batch settings
// (5s tick, 10s timeout) so app-side and platform-side timing aren't
