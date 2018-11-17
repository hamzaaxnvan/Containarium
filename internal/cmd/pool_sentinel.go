//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Sentinel selection for `pool join` (#699). A host joining a multi-region pool
// should dial its LOWEST-LATENCY sentinel, and latency can only be measured
// from the host. So the operator (or the control plane's "add compute" flow)
// passes the candidate sentinels and the host probes + self-selects.

const (
	defaultProbeAttempts = 3
	defaultProbeTimeout  = 2 * time.Second
)

// sentinelCandidate is one dialable sentinel, optionally labeled with a region.
type sentinelCandidate struct {
	Region string // "" when the operator passed a bare host:port
	Addr   string // host:port
}

// rttRow is a candidate's probe result — RTT on success, Err on failure.
type rttRow struct {
	Cand sentinelCandidate
	RTT  time.Duration
	Err  error
}

// parseSentinelCandidates parses repeated --sentinel values, each either
// "host:port" (unlabeled) or "region=host:port". Pure.
func parseSentinelCandidates(vals []string) ([]sentinelCandidate, error) {
	out := make([]sentinelCandidate, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		var c sentinelCandidate
		// Split on the FIRST '=' so a region label is optional; host:port has no '='.
		if i := strings.Index(v, "="); i >= 0 {
			c.Region = strings.TrimSpace(v[:i])
			c.Addr = strings.TrimSpace(v[i+1:])
			if c.Region == "" {
				return nil, fmt.Errorf("invalid --sentinel %q: empty region before '='", v)
			}
		} else {
			c.Addr = v
		}
		if c.Addr == "" {
			return nil, fmt.Errorf("invalid --sentinel %q: empty host:port", v)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no sentinel candidates")
	}
	return out, nil
}

// pickLowestRTT returns the candidate with the lowest successful median RTT.
// Candidates that failed to probe are excluded; if none succeeded it returns an
// error listing what was tried (never silently picks nothing). Pure.
func pickLowestRTT(rows []rttRow) (sentinelCandidate, error) {
	best := -1
	for i, r := range rows {
		if r.Err != nil {
			continue
		}
		if best < 0 || r.RTT < rows[best].RTT {
			best = i
		}
	}
	if best < 0 {
		var tried []string
		for _, r := range rows {
			tried = append(tried, fmt.Sprintf("%s (%v)", r.Cand.Addr, r.Err))
		}
		return sentinelCandidate{}, fmt.Errorf("all sentinel candidates failed to probe: %s", strings.Join(tried, "; "))
	}
	return rows[best].Cand, nil
