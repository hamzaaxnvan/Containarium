package auth

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthorizeTrackerTenant is AuthorizeTenant for the tracker verbs a skill
// run reaches from inside its box, with one extra way to pass.
//
// Why this exists rather than reusing AuthorizeTenant (#2268). A run token
// is minted for the BOX's subject — agent-<skill-id>, no roles — with the
// dispatching tenant recorded only as its `act` audit claim and, since
// #2268, as its `run_tenant` claim. The in-box tracker_* tools name the
// tenant from the dispatch input as req.Username, which AuthorizeTenant
// compares against the subject: the box never equals the tenant, so every
// tracker verb refused every real run (the hand-built subjects in the
// server tests hid it). Changing the subject instead would hand a run the
// tenant's whole API surface (containers, secrets, ...), and would break
// the box-as-subject checks the run log and A2A paths rely on. The rule
// here is the narrow one the design note asks for:
//
//   - A caller acting on ITS OWN tenant, or holding the admin role, passes
//     exactly as with AuthorizeTenant — operator/human tokens are unchanged.
//   - A RUN token (run_id claim present) passes for the one tenant its
//     run_tenant claim names: the tenant whose own token started the run
//     and whose connection its tracker_conn claim was validated against.
//     Any other tenant is refused — a run for tenant A on tenant B's
//     connection is the IDOR this check exists to stop.
//   - A run_tenant claim with no run_id is ignored: only a run token may
//     act for a tenant it is not.
//
// Both claims come from the verified token (the HTTP middleware and the
// gateway annotator carry them, and both metadata keys are reserved so no
// client can set them), never from a request field.
//
