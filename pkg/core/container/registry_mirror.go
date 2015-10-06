package container

import (
	"fmt"
	"os"
	"strings"
)

// Warm-image cache via bring-your-own registry mirror (#908).
//
// Every box has its own ephemeral podman storage, so a fresh box re-pulls its
// images from scratch — a multi-GB image over the WAN can blow a programmatic
// consumer's readiness budget (the OpenHands SDK gives 300s; a ~3.8GB pull took
// ~17min on a loaded host). Pull-through caching is a commodity, so Containarium
// does NOT run a registry: it ships only the thin box-side wiring. An operator
// points boxes at a mirror they already run (registry:2 proxy, Harbor, zot, a
// cloud AR mirror) via CONTAINARIUM_REGISTRY_MIRRORS, and box provisioning drops
// a non-destructive registries.conf.d entry so the box's podman resolves those
// upstreams through the LAN mirror first. First box pulls through (WAN, once);
// every box after pulls over the LAN. Fails safe: no config -> no drop-in ->
// today's behavior; mirror down -> podman falls back to the upstream.
//
// See docs/WARM-IMAGE-CACHE-DESIGN.md.

// envRegistryMirrors is the host/daemon env var declaring registry mirrors as a
// comma-separated list of `upstream=mirror` pairs, e.g.
//
//	CONTAINARIUM_REGISTRY_MIRRORS="docker.io=http://mirror.lan:5000,ghcr.io=http://mirror.lan:5000"
//
// The mirror may carry a scheme: `http://` marks it insecure (plain-HTTP LAN
// mirror); `https://` or a bare host:port is treated as secure.
const envRegistryMirrors = "CONTAINARIUM_REGISTRY_MIRRORS"

// registryMirrorsPath is the box-side drop-in. podman merges every file in
// registries.conf.d over the base registries.conf, so this never clobbers the
// image's defaults (unqualified-search-registries etc.).
const registryMirrorsPath = "/etc/containers/registries.conf.d/00-containarium-mirrors.conf"

// registryMirrorsDir is the drop-in directory (podman ships it, but a minimal
// base image may not — we mkdir -p before writing).
const registryMirrorsDir = "/etc/containers/registries.conf.d"
