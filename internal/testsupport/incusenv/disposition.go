// Package incusenv asserts that a real, usable Incus is present before an
// Incus-backed integration test runs, and decides what to do when one is not
// (#1332).
//
// The decision is the whole point of the package. On a developer's laptop or
// a container dev box there is legitimately no Incus, and skipping is right.
// In the CI lane that exists to *demonstrate* the daemon's create path, a
// skip is the failure mode: the job reports green having proven nothing,
// which is exactly what let #1195 merge on a check that could not fail
// (#1234). So the lane sets CONTAINARIUM_REQUIRE_INCUS and the same missing
// environment becomes a hard failure naming the step that was missing.
//
// This file carries no build tag on purpose: the switch itself is testable —
// and provably able to fail — in the ordinary unit suite, without an Incus
// anywhere. The code that touches a real Incus lives behind the `incus` tag.
package incusenv

import (
	"os"
	"strings"
)

// RequireEnv is the environment variable that turns "no usable Incus here"
// from a skip into a failure. Set it in any lane whose job is to prove the
// Incus path actually runs.
const RequireEnv = "CONTAINARIUM_REQUIRE_INCUS"
