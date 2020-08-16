package waf

import (
	"bytes"
	"net"
)

// Verdict is an Inspector's decision about a connection's request head.
type Verdict struct {
	Block    bool
	RuleID   uint16 // the matched rule/signature id (0 if none)
	RuleName string // human label for the audit log
}

// Inspector examines the start of a steered connection's payload — the request
// head, REASSEMBLED across TCP segments — and decides whether to block. The
// reassembly is the value over Tier 2's in-kernel scan, which only ever sees a
// single packet (a signature split across segments evades it). The interface is
// the seam a real WAF engine (Coraza + the OWASP CRS) plugs into later, behind a
// `waf` build tag; BuiltinInspector is the reference implementation that ships
// now with no extra dependency.
type Inspector interface {
	Inspect(head []byte) Verdict
}

// maxHeadBytes bounds how much of a connection's start is buffered for
// inspection — enough for an HTTP request line + headers (where header-borne
// exploits like Log4Shell live), capped so a slow/huge request can't exhaust
// memory. Bytes past the head still flow through, uninspected (documented best-
// effort limit; full body inspection is a WAF-engine refinement).
const maxHeadBytes = 16 * 1024
