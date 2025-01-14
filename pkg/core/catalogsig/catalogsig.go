// Package catalogsig provides an optional, offline provenance check for
// external skill/crew catalogs loaded from disk (#648). It verifies a detached
// ed25519 signature of each catalog file against a set of operator-configured
// trusted public keys before the file is parsed and merged.
//
// The check is opt-in. When require-signed mode is off (the default), callers
// load catalogs unsigned exactly as before — this is correct for the
// self-authored / BYOA path where the operator points the env var at a
// directory they wrote themselves. The check only matters once a catalog is
// distributed from outside the operator's own tree, where "did this manifest
// come from a source I trust, intact" is a real question.
//
// Verification performs no network I/O: the trusted keys are configured
// locally, so it works for air-gapped installs.
package catalogsig

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Environment variables that configure the provenance check.
const (
	// EnvRequireSigned, when truthy ("1"/"true"/"yes"), turns on require-signed
	// mode: every external catalog file must carry a valid detached signature.
	EnvRequireSigned = "CONTAINARIUM_CATALOG_REQUIRE_SIGNED"
