package server

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Phase 3.1 follow-up — image digest pinning (audit
// B-HIGH-1, supply-chain hardening half).
//
// The allowlist (image_allowlist.go) keeps the daemon from
// pulling images from arbitrary registries. Digest pinning
// adds a second axis: even within an allowed registry, the
// operator forces every CreateContainer request to name
// the EXACT image bytes — not "latest", not a tag that
// could be re-pushed.
//
// Wire shape: operator sets
//
//   CONTAINARIUM_REQUIRE_IMAGE_DIGEST=true
//
// and from that point on the daemon refuses any image
// reference that doesn't carry `@sha256:<64-hex>`. The
// daemon doesn't verify the digest against the registry's
// content (that requires Incus-side support and is the
// "real" half of digest pinning, tracked separately) — but
// it does force the operator to write a specific digest
// down. The supply-chain attacker who hopes a tagged image
// will quietly be re-pulled with malicious bytes is denied
// the ambiguity they need.
//
// Default is off — turning enforcement on is a flag flip,
// not a code change.

const requireImageDigestEnv = "CONTAINARIUM_REQUIRE_IMAGE_DIGEST"

var (
	imageDigestOnce sync.Once
	imageDigestReq  bool

	// sha256Pattern matches a single base64-encoded SHA-256
	// hex digest as it appears in an OCI image reference:
	// 64 lowercase hex chars after a literal `sha256:`.
	sha256Pattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func loadDigestRequired() bool {
	imageDigestOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(requireImageDigestEnv))
		if raw == "" {
			imageDigestReq = false
			return
		}
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on":
			imageDigestReq = true
			log.Printf("[image-digest] required: every CreateContainer must name a `@sha256:<64hex>` digest (audit B-HIGH-1 follow-up)")
		default:
			imageDigestReq = false
			log.Printf("WARNING: %s=%q is unrecognized (expected 1/true/yes/on); digest pinning STAYS OFF", requireImageDigestEnv, raw)
		}
	})
