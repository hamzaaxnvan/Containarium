package server

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// cpuReservationTTL bounds how long an admitted request's reservation
// (#1588) can outlive its caller before expiring on its own. Generous
// enough to cover a slow create (image pull, disk allocation) well past
// what's actually observed in practice, but bounded so a caller whose
// completion this package can't observe — the k8s Box-CR path, which
// hands off to the operator's own reconciliation — can't wedge a host's
// admission gate forever.
const cpuReservationTTL = 10 * time.Minute

// cpuReservation is one tenant's outstanding admitted-but-not-yet-committed
// CPU request. token disambiguates a release call from a stale/superseded
// reservation for the same username (e.g. two overlapping admission checks
// for one tenant) — release only clears the entry if it still holds the
// token the release closure was handed.
type cpuReservation struct {
	cores     float64
	expiresAt time.Time
	token     uint64
}

// CPU capacity admission (#1029 direction 2).
//
// A whole-core `limits.cpu` only bounds WHICH cores a container can see, not
// how much CPU time it may consume; #1034 made every request also carry a hard
// CFS quota, but that still lets an operator pack far more committed cores onto
// a host than it physically has ("~14× overcommit" was observed live on an
// 8-core host holding many `limits.cpu=8` tenants). CPU is compressible, so
// *some* overcommit is fine and even desirable — but unbounded overcommit is
// how one busy tenant starves every co-located one.
//
// This gate refuses (or, in advisory mode, merely warns about) a create that
// would push a host's committed cores past `physicalCores × factor`. It is:
//
//   - Off by default. `factor <= 0` disables it entirely — no list, no read,
//     no behavior change. Existing already-overcommitted fleets keep working
//     until an operator opts in.
//   - Rollout-friendly. `factor > 0` with enforce=false logs what it *would*
//     reject without blocking, so an operator can watch a real fleet for a
//     while (mirroring how eBPF enforcement was rolled out observe-first)
//     before flipping enforce=true.
//   - Fail-open. If the host's core count or container list can't be read, the
//     create proceeds — a capacity check must never be the reason a box can't
//     be made.
//
// The gate is inherently per-host: it runs on the daemon that will actually
// create the box. Peer-routed creates are forwarded to the target peer's own
// daemon, which runs its own gate against its own host, so no cross-host
// capacity view is needed here.

// admitCPURequest is the pure policy core: adding requestCores to a host that
// already commits committedCores of physicalCores, at the given overcommit
// factor, fits iff the projected total stays within physicalCores × factor.
// It also returns the projected overcommit ratio (committed+request : physical)
// for logging. A non-positive physicalCores means "capacity unknown" and
// always fits — the caller fails open.
func admitCPURequest(physicalCores, committedCores, requestCores, factor float64) (ratio float64, fits bool) {
	if physicalCores <= 0 {
		return 0, true
	}
	projected := committedCores + requestCores
	ratio = projected / physicalCores
	return ratio, projected <= physicalCores*factor
}

// noopRelease is returned from every admitCPUCapacity path that never
// reserved anything (gate disabled, fail-open, or a rejected request) — safe
// for every caller to defer unconditionally regardless of which path ran.
func noopRelease() {}
