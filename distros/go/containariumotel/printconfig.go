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
	popts := printOptions{redactBearer: true}
	for _, o := range opts {
		o(&popts)
	}
	cfg := ConfigFromEnv()
	return printConfig(w, cfg, popts.redactBearer)
}

// PrintOption configures PrintConfig.
type PrintOption func(*printOptions)

type printOptions struct {
	redactBearer bool
}

// WithoutHeaderRedaction disables the default Authorization /
// X-API-Key redaction in PrintConfig output. Don't use this in any
// path that touches logs or external systems.
func WithoutHeaderRedaction() PrintOption {
	return func(o *printOptions) { o.redactBearer = false }
}

func printConfig(w io.Writer, cfg DistroConfig, redact bool) error {
	var sb strings.Builder
	sb.WriteString("# containariumotel resolved config\n")
	fmt.Fprintf(&sb, "distro_version       : %s\n", version)
	fmt.Fprintf(&sb, "endpoint             : %s\n", or(cfg.Endpoint, "<unset>"))
