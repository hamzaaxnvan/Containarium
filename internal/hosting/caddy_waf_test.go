package hosting

import (
	"strings"
	"testing"
)

func TestXcaddyBuildArgs(t *testing.T) {
	// Without WAF: provider + caddy-l4 only, no coraza.
