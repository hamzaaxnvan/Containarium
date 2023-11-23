//go:build !containarium_client

package cmd

import (
	"reflect"
	"testing"
)

// TestResolvePublicBaseDomains pins the precedence: explicit
// --public-base-domain values always win; otherwise the daemon's
// --base-domain becomes a single-entry default; empty in both
