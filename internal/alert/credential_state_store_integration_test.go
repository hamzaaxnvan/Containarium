//go:build integration

// Integration coverage for the credential-watch state store (#2371): the
// "once per flip, not again after a restart" guarantee is only as good as
// this table's round trip.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/alert/
package alert

import (
	"context"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func credentialStateStore(t *testing.T) *PGCredentialStateStore {
	t.Helper()
