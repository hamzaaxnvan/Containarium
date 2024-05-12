package sentinel

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sentinel self-profiling (#1352).
//
// The sentinel had no way to produce a heap profile from a running process.
// When #1349 OOM-killed it at 565 MB RSS — after ~27 days of ~18 MB/day growth
// — the process was gone before anything could be captured, and the leak could
// not be located without building an instrumented binary and waiting weeks for
// it to reproduce in production. This endpoint is that missing diagnostic.
//
// Two deliberate choices:
//
//  1. runtime/pprof, NOT net/http/pprof. The latter's init() registers
//     /debug/pprof/* on http.DefaultServeMux merely by being imported, blank
//     or named. Nothing serves DefaultServeMux in this binary today, so the
//     exposure would be invisible — right up until someone adds an
//     http.ListenAndServe(addr, nil) for something unrelated and silently
//     publishes an unauthenticated heap dump of the daemon, the agent-box, or
//     the sentinel (all the same binary). Wiring runtime/pprof by hand costs
//     ~40 lines and has no global side effects. TestPprofNotRegisteredOnDefaultServeMux
//     fails if the import ever comes back.
//
//  2. Gated on the ADMIN secret, not the cluster-wide daemon HMAC secret. A
//     heap profile contains whatever the process is holding: tunnel-join
//     tokens, TLS private key material, in-flight request bodies. Every
//     backend daemon in the cluster holds the daemon secret for
//     keysync/certsync, so gating there would let any one of them dump the
//     sentinel's memory. This is an operator/control-plane capability, gated
//     like /sentinel/tunnel-tokens and /peer/ (#1102, #733).
//
// The path stays at the conventional /debug/pprof/ rather than under
// /sentinel/*: it is what operators and `go tool pprof` expect, and it is the
// exact path internal/pentest probes for an exposed profiler — so our own
// scanner now validates this gate instead of missing a route it doesn't know
// about.

const (
	// pprofDefaultCPUSeconds matches net/http/pprof's default.
	pprofDefaultCPUSeconds = 30

	// pprofMaxCPUSeconds bounds ?seconds= so a request cannot outlive the
	// binary server's 120s WriteTimeout (which would drop the response after
	// paying the whole profiling cost) or pin the profiler indefinitely.
	pprofMaxCPUSeconds = 60

	pprofPathPrefix = "/debug/pprof/"
)

// PprofHandler serves runtime profiles. Mount it behind the admin-secret HMAC
// middleware — it is never safe to expose unauthenticated.
//
//	GET /debug/pprof/              index of available profiles
