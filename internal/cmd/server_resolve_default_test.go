//go:build !containarium_client

package cmd

import "testing"

// TestResolveServerAddr_Default pins that containariumd's resolveServerAddr
// is the identity function: no credentials-file fallback, even with an
// empty input. An operator's host must never get silently repointed from
// local Incus to a stale remote fleet by a leftover default_server (#1776).
