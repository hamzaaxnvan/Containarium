//go:build windows && !containarium_client

package cmd

import "fmt"

// Collaborator management needs a local Postgres store
// (getPostgresConnString, in turn internal/server) which does not compile
