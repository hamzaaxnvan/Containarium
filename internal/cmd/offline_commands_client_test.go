//go:build containarium_client

package cmd

import "testing"

// TestOfflineCommands_SucceedWithNoServerConfigured is #1776's Done-when:
// cert generate, token inspect, token generate, and version must all
// succeed under containarium_client with no --server, no
// CONTAINARIUM_SERVER, and no credentials.json anywhere — because none of
// them ever constructs a client. This is what "the server requirement
// lives at the dial seam, not in pre-run" means in practice: adding
// resolveServerAddr to PersistentPreRunE (server_resolve_client.go) must
// not turn into an accidental pre-run gate on these.
