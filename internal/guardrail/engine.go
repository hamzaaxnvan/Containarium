// Package guardrail drives a detection engine over a set of text units,
// redacts what it flagged, re-scans the result as the gate, and signs an
// attestation a consumer can verify before it touches the data
// (docs/architecture/guardrail.md).
//
// The package detects nothing itself. Every finding comes from an Engine;
// the in-tree RulesEngine is a reference implementation so the flow is
// runnable without any external process, not a product detector.
package guardrail

import (
	"context"

