// Package reqrate computes per-container HTTP request rates from the Caddy
// edge's structured (JSON) access log — the source for #231's request-rate
// plane. See docs/REQUEST-RATE-PLANE-DESIGN.md.
//
// The package is the offline, unit-tested building block (slice 1): it parses
// access-log lines, accumulates per-host counts over an interval, and joins
// hosts to containers using the route table + container list the collector
// already holds. The live tailer + collector wiring (which turn this into
// emitted metrics) are later slices; nothing here reads a file or talks to
// Caddy, so all of it is testable without a running edge.
package reqrate

import (
	"encoding/json"
	"net"
	"strings"
)

// accessEntry is the subset of a Caddy JSON access-log record we need. Caddy
// writes one such object per handled request when the server has a `logs`
// configuration with the json encoder. Fields we don't use are ignored by the
// decoder.
type accessEntry struct {
	Request struct {
		Host string `json:"host"`
	} `json:"request"`
}

// ParseHost extracts the (lower-cased, port-stripped) request host from one
