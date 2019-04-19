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
