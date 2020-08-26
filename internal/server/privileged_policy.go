package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/footprintai/containarium/internal/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Privileged-Podman authorization policy (audit A-HIGH-3).
//
// The pre-Phase-3 behavior: `enable_podman=true` on CreateContainer
// silently set `security.privileged=true` and `lxc.apparmor.profile=unconfined`
// on the LXC, granting the user effective host-root inside the
// container. Anyone with CreateContainer permission could
// elevate to a privileged container — by design, but a HIGH
// severity privilege-escalation primitive when tenants aren't
// trusted.
//
// The new policy:
//
//   CONTAINARIUM_PRIVILEGED_PODMAN_POLICY = all | admin-only | disabled
//
//   all         (default when unset) — pre-Phase-3 behavior: any
//                caller that requests podman gets a privileged
//                container. Backwards-compatible.
//   admin-only  — only callers with the admin role get privileged
//                podman; non-admin requests are rejected with
//                PermissionDenied. Recommended for multi-tenant
//                deployments.
//   disabled    — no caller gets a privileged container, even
//                admins. Podman falls back to unprivileged mode
//                (limited functionality). For paranoid setups.
//
// The default stays `all` for backwards compat during rollout.
// Operators set the env var to `admin-only` once they've
// verified that no non-admin workflow requires privileged Podman.
//
// The value is normalised before matching (surrounding whitespace
// trimmed, lower-cased), so `ALL`, `Admin-Only` and `disabled ` select
// their policy. Only an unset variable keeps the `all` default. A value
// that is set but blank or still unrecognised after normalisation
// (`none`, `off`, `admin_only`, a typo) fails closed (#2299, #2345):
// the daemon refuses to start (validatePrivilegedPolicyEnv,
// called from NewDualServer), and any path that reads the policy
// without that check treats it as `disabled`. A mistyped restrictive
// setting must never silently become the most permissive one.
//
// A future iteration can split `enable_podman` from
// `enable_privileged` in the proto contract; this PR keeps the
// proto shape and gates the implied privilege escalation server-side.

const privilegedPolicyEnv = "CONTAINARIUM_PRIVILEGED_PODMAN_POLICY"

type PrivilegedPolicy int
