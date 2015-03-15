package container

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// EnvFile describes a dotenv file the daemon delivers into a container for
// docker-compose `env_file:` consumption. Apps running directly in the LXC
// inherit env from the Incus config; nested docker / docker-compose apps do
// NOT, so they reference a file like this via `env_file:` instead.
//
// This is the generalized form of the original OTel-only mechanism (#370).
// Each consumer (OTel monitoring, tenant secrets, …) supplies its own
// path / mode / header; the render + write + remove plumbing is shared.
//
// Deliberately scoped to the dotenv-file delivery target ONLY. The LXC
// config-env path (Manager.SetEnv → environment.<NAME>) has different
// semantics (inherited by LXC processes, visible in `incus config show`)
// and is intentionally NOT folded in here.
type EnvFile struct {
	// Path is the absolute file path inside the container, e.g.
	// "/etc/containarium/otel.env".
	Path string

	// Mode is the octal file mode as a string, e.g. "0644" for
