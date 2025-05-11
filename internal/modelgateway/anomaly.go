package modelgateway

import (
	"fmt"
	"net"
	"time"
)

// Per-tenant anomaly signals.
//
// The design constraint from the issue is "per-tenant baseline, not absolute
// thresholds", and it is the important part. An absolute threshold is either so
// high that a stolen token can spend freely under it, or so low that it fires on
// the tenant's ordinary Monday morning. A tenant compared against its own recent
// history has neither problem: the same 10x jump is signal for a steady tenant
// and noise for a bursty one, and the detector learns which is which without
// anyone tuning a number per customer.
//
// Signals are advisory. They feed the policy ladder, which decides what to do —
// nothing here blocks a request on its own.

// AnomalyConfig tunes the signals. The zero value disables detection, so a
// deployment that configures quotas but not anomaly detection gets quota
// enforcement alone.
type AnomalyConfig struct {
	Enabled bool `json:"enabled"`

	// BaselineWindow is the period a tenant's token rate is measured over
	// before being folded into its baseline. Default 5m.
	BaselineWindow time.Duration `json:"baseline_window"`

	// BaselineAlpha is the EWMA weight for a new sample. Default 0.2 — roughly
	// a five-sample memory, so the baseline tracks a genuine change in workload
	// within half an hour but is not moved much by one spike.
	BaselineAlpha float64 `json:"baseline_alpha"`

	// BaselineMinSamples is how many windows must be observed before the
	// baseline is compared against. Below this the tenant is still being
	// learned and the signal stays silent. Default 5.
	BaselineMinSamples int `json:"baseline_min_samples"`

	// BaselineFactor is how many times its own baseline a tenant's current rate
	// must reach to be anomalous. Default 8 — deliberately loose, because the
	// cost of a false positive here is throttling a paying customer's agent.
	BaselineFactor float64 `json:"baseline_factor"`

	// NoveltyTTL is how long a model id, upstream path, or source network stays
	// "known" for this tenant. Default 24h.
	NoveltyTTL time.Duration `json:"novelty_ttl"`

	// ConcurrentSourceNets is how many distinct source networks may use one
	// tenant's tokens within ConcurrentWindow before it is treated as
