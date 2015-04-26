package server

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// Image-registry allowlist for CreateContainer (audit B-HIGH-1).
//
// `req.Image` flows unmodified to the Incus runtime, which will
// pull whatever URL or remote: prefix it sees. Without a guard,
// a caller can:
//
//   - Pull from a typosquatted Linux Containers mirror that
//     swaps in malicious base images.
//   - Use a `https://internal-service.example/...` URL as an
//     SSRF probe (the daemon's network position lets it reach
//     internal services the caller can't).
//   - Pull oversized junk to exhaust the host's disk.
//
// CONTAINARIUM_ALLOWED_IMAGE_REGISTRIES is a comma-separated list
// of registry prefixes the daemon will accept. Examples:
//
//   ubuntu,images,incus              # the three Linux Containers remotes
//   ubuntu,docker:registry.example   # only ubuntu + one private registry
//
// Empty value means "accept anything" — pre-Phase-3 behavior. The
