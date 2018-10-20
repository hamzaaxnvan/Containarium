package containariumotel

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// redactedHeaderKeys (lowercased) get their values replaced with
// "<redacted>" in PrintConfig output.
var redactedHeaderKeys = map[string]struct{}{
	"authorization": {},
	"x-api-key":     {},
}

// PrintConfig writes a human-readable rendering of the resolved
// telemetry config to w. Mirrors the Python distro's
// `containarium-instrument --dry-run` output so operators see the
// same thing across languages.
//
// Bearer tokens and API keys are redacted by default. Pass
// WithoutHeaderRedaction to disable redaction (rarely useful — only
// when debugging the exact wire format and you've verified no token
// leaks).
func PrintConfig(_ context.Context, w io.Writer, opts ...PrintOption) error {
