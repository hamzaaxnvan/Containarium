package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/pkg/core/box"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Platform-started runs on a box with a recorded tool-access level execute as a
// dedicated unprivileged OS user instead of the box's owner (#2944, slice 1b).
// The owner's login keeps its sudo; this user never has any, so a policy file
// only root can write (a later slice) is a real boundary for the run. A box
// with NO recorded level keeps today's path exactly.
//
// What a run reads from "its" home — the agent CLI, the MCP config, the credential
// helper — lives under the OWNER's $HOME on a bootstrapped box, so the run user
// gets its own staged copy of that NON-Claude configuration.
//
// The platform never reads, copies, refreshes or stores a Claude login (Anthropic's
// terms forbid a platform collecting, storing or intermediating one; see #2036).
// The run user signs in ONCE through Anthropic's own flow, as itself; the platform
// only checks, by the existing credential-status probe, that a usable sign-in is
// there, and refuses the run with an actionable message when it is not (never
// signed in, or expired) so the box's owner can be told to sign in again.

// boxRunUnprivilegedUser is the OS user such runs execute as. A fixed name: the
// daemon creates it lazily on the first such run, so existing boxes need no backfill.
const boxRunUnprivilegedUser = toolaccess.RunUser

// boxRunRecordedToolAccess reports the tool-access level recorded for username's
// box ("" = none recorded, the legacy path). The fn seam is for tests; production
// reads the Incus config through the box backend. A read error is returned, never
// swallowed into "", so a transient failure cannot silently demote a run to the
// owner's identity.
func (s *ContainerServer) boxRunRecordedToolAccess(ctx context.Context, username string) (string, error) {
	if s.boxRunToolAccess != nil {
		return s.boxRunToolAccess(ctx, username)
	}
	if s.manager == nil && s.boxBackend == nil {
		return "", nil
	}
	info, err := s.boxes().Get(ctx, box.BoxRef{Tenant: username})
	if err != nil {
		return "", err
	}
	if info == nil {
