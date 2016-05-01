package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Phase 3.1 Phase-A — simplestreams index resolver. Audit
// B-HIGH-1 deeper half. See
// docs/security/IMAGE-DIGEST-VERIFY-DESIGN.md for the full
// design.
//
// This file builds the resolver only. It does NOT wire
// into CreateContainer — that's Phase B. Phase A's
// contract is narrow: given a simplestreams server and an
// image alias, return the set of SHA-256 digests the
// registry has published for that alias. The caller (in
// Phase B) checks membership of the operator-supplied
// `@sha256:` digest.
//
// We deliberately avoid go-incus or a third-party
// simplestreams parser. Reasons:
//
//   - Our usage is one endpoint and one JSON shape; ~100
//     lines of Go.
//   - The full simplestreams library carries product-
//     resolution / signature-validation logic we don't
//     need (Incus already does both during the actual
//     pull). Phase A is just "match the digest in the
//     index;" Phase B + C handle the rest.
//   - One fewer dependency in govulncheck and the supply
//     chain.
//
// Wire shape (subset of the simplestreams products:1.0
// schema):
//
//   GET <server>/streams/v1/images.json
//   →  {
//        "format": "products:1.0",
//        "products": {
//          "ubuntu:24.04:amd64:default": {
//            "aliases": "24.04,noble,ubuntu/24.04",
//            "arch": "amd64",
//            "versions": {
//              "20240519_07:42": {
//                "items": {
//                  "lxd.tar.xz":           {"ftype": "...", "sha256": "..."},
//                  "root.squashfs":        {"ftype": "...", "sha256": "..."},
//                  "lxd_combined.tar.gz":  {"ftype": "...", "sha256": "..."}
//                }
//              },
//              "20240520_07:42": {...}
//            }
//          }
//        }
//      }
//
// "aliases" is a comma-separated list because each product
// can have several human-readable shortnames.

// indexPath is the simplestreams products:1.0 endpoint
// every supported remote serves.
const indexPath = "/streams/v1/images.json"

// StreamsResolver fetches and caches the simplestreams
// index for one or more registry servers. Construction is
// cheap; the network call lands on the first Resolve.
//
// Phase B+ follow-up: the resolver now caches each
// fetched index in memory for `cacheTTL`. A CreateContainer
// burst against the same registry (e.g., 50 containers
// pulled from images.linuxcontainers.org during a fleet
// rollout) collapses into a single index fetch. TTL
// defaults to 5 minutes — a window short enough that a
// freshly-published image becomes verifiable within one
// pull cycle, but long enough to absorb operator bursts.
//
// Cache is keyed by server URL. Each entry holds the raw
// imagesIndex plus its fetch time. Expired entries are
// re-fetched on the next Resolve; a parallel re-fetch
// during expiry is acceptable (worst case: one extra
// network call) and avoids the singleflight machinery
// that would otherwise be needed.
type StreamsResolver struct {
	client *http.Client

	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cachedIndex
}

type cachedIndex struct {
	idx       imagesIndex
	fetchedAt time.Time
}

// defaultCacheTTL is the cache horizon when callers don't
// override it. Five minutes balances "operator burst
// absorption" against "freshly-published image visibility."
const defaultCacheTTL = 5 * time.Minute

// NewStreamsResolver builds a resolver. timeout caps every
// network call to the registry index; 10s is the
// suggested default (matches Vault/GCP KMS timeouts and
// gives room for simplestreams indexes that have grown to
// a few MB).
//
// Cache TTL defaults to 5 minutes. Use NewStreamsResolverWithCacheTTL
// to override (tests pass 0 to disable caching).
func NewStreamsResolver(timeout time.Duration) *StreamsResolver {
	return NewStreamsResolverWithCacheTTL(timeout, defaultCacheTTL)
}

// NewStreamsResolverWithCacheTTL is the full constructor
// — same as NewStreamsResolver but lets callers control
// cache TTL. Pass 0 to disable caching (each Resolve
// fetches fresh).
func NewStreamsResolverWithCacheTTL(timeout, cacheTTL time.Duration) *StreamsResolver {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if cacheTTL < 0 {
		cacheTTL = 0
	}
	return &StreamsResolver{
		client:   &http.Client{Timeout: timeout},
		cacheTTL: cacheTTL,
		cache:    make(map[string]cachedIndex),
	}
}

// imagesIndex is a subset of the simplestreams products:1.0
// schema, decoded just enough to walk the (alias →
// version → item.sha256) chain. Unknown fields are
// ignored by encoding/json.
type imagesIndex struct {
	Format   string                  `json:"format"`
	Products map[string]productEntry `json:"products"`
}

type productEntry struct {
	// Aliases is the human-readable shortname list,
	// comma-separated. Examples:
	//   "24.04,noble"
	//   "ubuntu/24.04"
	// We normalize on parse — split, trim, lowercase.
	Aliases string `json:"aliases"`

	Arch string `json:"arch"`

	// Versions is keyed by build timestamp
	// ("20240519_07:42"). The map order from JSON is
	// non-deterministic; we sort by key when walking.
	Versions map[string]versionEntry `json:"versions"`
}

type versionEntry struct {
	Items map[string]itemEntry `json:"items"`
}

type itemEntry struct {
	FType  string `json:"ftype"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Path   string `json:"path"`
}

// ResolveImageDigests fetches the simplestreams index at
// `server` and returns every SHA-256 digest the registry
// has published for any version of any product whose
// aliases match `alias`. The set semantics is intentional:
// each version publishes multiple items (rootfs,
// metadata, combined), each carrying its own digest, and
// the operator's `@sha256:...` reference can point at any
// of them. The caller (Phase B) checks membership.
//
