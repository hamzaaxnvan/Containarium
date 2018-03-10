//go:build windows && !containarium_client

package cmd

import "fmt"

// Collaborator management needs a local Postgres store
// (getPostgresConnString, in turn internal/server) which does not compile
// on Windows — internal/server pulls in Unix-only file-ownership and eBPF
// code (see postgres.go's own !windows tag). This is the same "Windows is
// CLIENT-ONLY" shape the rest of cmd/containariumd's windows build already
// has via scattered !windows tags on individual files (see build-all's
// comment in the Makefile): these stubs keep that windows target
