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
	// impossible-concurrency. Default 3.
	//
	// This is the issue's "geo-exclusive simultaneous use" signal, approximated
	// without a geoip database: OSS ships no IP-to-location data and should not
	// grow a dependency on one, so the gateway counts distinct /24s (v4) or /48s
	// (v6) instead. It is a weaker signal — two datacenters in one region look
	// like two networks — but it catches the case that matters, a token in use
	// from somewhere it has never been while the legitimate box keeps working.
	ConcurrentSourceNets int           `json:"concurrent_source_nets"`
	ConcurrentWindow     time.Duration `json:"concurrent_window"`

	// AuthFailureRatio is the share of a tenant's recent requests that may be
	// rejected before the ratio itself is a signal — a token being probed
	// against models or providers it isn't scoped for. Default 0.3, requiring
	// at least AuthFailureMin observations so a single early failure is not a
	// 100% failure rate.
	AuthFailureRatio float64 `json:"auth_failure_ratio"`
	AuthFailureMin   int64   `json:"auth_failure_min"`
}

func (a AnomalyConfig) normalized() AnomalyConfig {
	if a.BaselineWindow <= 0 {
		a.BaselineWindow = 5 * time.Minute
	}
	if a.BaselineAlpha <= 0 || a.BaselineAlpha > 1 {
		a.BaselineAlpha = 0.2
	}
	if a.BaselineMinSamples <= 0 {
		a.BaselineMinSamples = 5
	}
	if a.BaselineFactor <= 1 {
		a.BaselineFactor = 8
	}
	if a.NoveltyTTL <= 0 {
		a.NoveltyTTL = 24 * time.Hour
	}
	if a.ConcurrentSourceNets <= 0 {
		a.ConcurrentSourceNets = 3
	}
	if a.ConcurrentWindow <= 0 {
		a.ConcurrentWindow = 5 * time.Minute
	}
	if a.AuthFailureRatio <= 0 {
		a.AuthFailureRatio = 0.3
	}
	if a.AuthFailureMin <= 0 {
		a.AuthFailureMin = 10
	}
	return a
}

// Signal is one fired detector. Severity is 0..1 and is what the ladder reads;
// Detail is operator-facing and carries no prompt or response content.
type Signal struct {
	Name     string  `json:"name"`
	Severity float64 `json:"severity"`
	Detail   string  `json:"detail"`
}

// Signal names, stable enough to alert on.
const (
	SignalRateBaseline    = "rate_baseline"
	SignalNewModel        = "model_switch"
	SignalNewEndpoint     = "endpoint_mix"
	SignalConcurrentNets  = "concurrent_source_nets"
	SignalAuthFailureRate = "auth_failure_ratio"
)

// evaluate runs every signal against a tenant's state. Caller holds ts.mu.
func (a AnomalyConfig) evaluate(ts *tenantState, now time.Time) []Signal {
	if !a.Enabled {
		return nil
	}
	var out []Signal

	// Cold start. A tenant the gateway has not learned yet has no "normal" to
	// deviate from, and its first call is novel by construction: the first model
	// it names has never been seen, and so has the first endpoint. Emitting
	// those escalates every new tenant on its very first request — and because
	// an escalated tenant stops feeding its baseline, it would then never learn
	// one, leaving the detector permanently stuck on a tenant it permanently
	// misjudges.
	//
	// So the novelty signals stay quiet until there is a history for something
	// to be novel against; the sets are still populated meanwhile, which is what
	// makes the history. The other two signals are NOT suppressed: a brand-new
	// tenant reaching the gateway from four networks, or failing auth on most of
	// its calls, is suspicious with or without a baseline.
	_, samples := ts.baseline.get()
	learning := samples < a.BaselineMinSamples

	// Rate against the tenant's own baseline.
	if base, n := ts.baseline.get(); n >= a.BaselineMinSamples && base > 0 {
		cur := ts.rateWindow.rate(now)
		if ratio := cur / base; ratio >= a.BaselineFactor {
			out = append(out, Signal{
				Name: SignalRateBaseline,
				// Saturates at 4x the trip factor, so a 100x runaway and a
				// 1000x one both read as "as bad as it gets" rather than
				// producing a meaningless unbounded number.
				Severity: clamp01((ratio - a.BaselineFactor) / (a.BaselineFactor * 3)),
				Detail: fmt.Sprintf("%.0f tok/s vs baseline %.0f (%.1fx, trips at %.0fx)",
					cur, base, ratio, a.BaselineFactor),
			})
		}
	}

	// A model this tenant has not used within the novelty TTL. The issue calls
	// this "model-tier switch": the observable shape of a stolen token is
	// someone reaching for the most capable model available, which for a tenant
	// with a settled workload is a new model id.
	if ts.pendingNewModel != "" && !learning {
		out = append(out, Signal{
			Name:     SignalNewModel,
			Severity: 0.5,
			Detail:   "first use of model " + ts.pendingNewModel + " in " + a.NoveltyTTL.String(),
		})
	}

	// An upstream path shape this tenant has not used — the endpoint-mix change.
	// A box that only ever ran chat completions suddenly listing models or
	// hitting embeddings is a different program than the one we provisioned.
	if ts.pendingNewEndpoint != "" && !learning {
		out = append(out, Signal{
			Name:     SignalNewEndpoint,
			Severity: 0.4,
			Detail:   "first use of endpoint " + ts.pendingNewEndpoint + " in " + a.NoveltyTTL.String(),
		})
	}

	// One tenant's token in use from more networks than a box population
	// explains.
	if n := ts.sourceNets.len(now); n > a.ConcurrentSourceNets {
		out = append(out, Signal{
			Name:     SignalConcurrentNets,
			Severity: clamp01(float64(n-a.ConcurrentSourceNets) / float64(a.ConcurrentSourceNets)),
			Detail: fmt.Sprintf("%d distinct source networks within %s (allowed %d)",
				n, a.ConcurrentWindow, a.ConcurrentSourceNets),
		})
	}

	// Rejected-request ratio: a token being probed rather than used.
	//
	// Reads authDenials only. Policy denials are excluded on purpose: counting
	// the ladder's own refusals here would make a throttled tenant look like a
	// probed one and escalate it further on the strength of our own enforcement.
	w := ts.rateWindow.sum(now)
	if total := w.calls + w.authDenials; total >= a.AuthFailureMin {
		if ratio := float64(w.authDenials) / float64(total); ratio >= a.AuthFailureRatio {
			out = append(out, Signal{
				Name:     SignalAuthFailureRate,
				Severity: clamp01(ratio),
				Detail:   fmt.Sprintf("%d/%d recent requests rejected (%.0f%%)", w.authDenials, total, ratio*100),
			})
		}
	}
	return out
}

// score reduces signals to the ladder's input: the worst single severity, nudged
// up when several independent detectors agree.
//
// Max rather than sum because the signals are not independent evidence of the
// same magnitude — a stolen token typically trips new-model AND new-network AND
// rate at once, and summing would let three weak coincidences outrank one strong
// detection. The corroboration bonus keeps agreement meaningful without letting
// it dominate.
func score(sigs []Signal) float64 {
	var max float64
	for _, s := range sigs {
		if s.Severity > max {
			max = s.Severity
		}
	}
	if len(sigs) > 1 {
		max += 0.1 * float64(len(sigs)-1)
	}
	return clamp01(max)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// sourceNet reduces a remote address to the network the concurrency signal
// counts: /24 for IPv4, /48 for IPv6. Returns "" when the address can't be
// parsed, which the caller treats as "no signal" rather than as a distinct
// network — an unparseable peer address must not manufacture an anomaly.
func sourceNet(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return net.IP(append(net.IP(nil), v4[0], v4[1], v4[2], 0)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}
