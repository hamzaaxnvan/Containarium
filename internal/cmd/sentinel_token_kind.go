//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// sentinelTokenKind selects which credential `sentinel register-token` and
// `deregister-token` operate on (#2415). A typed pflag.Value rather than a
// string flag plus a comment listing the allowed values: an unknown kind is
// rejected at flag parse, before any request is built.
type sentinelTokenKind string

const (
	// sentinelTokenKindTunnelJoin is the original behavior (#799): a BYOC
	// tunnel-join token. It stays the default so existing invocations and
	// control-plane callers are byte-for-byte unchanged.
	sentinelTokenKindTunnelJoin sentinelTokenKind = "tunnel-join"
	// sentinelTokenKindAuditIngest registers a backend-scoped audit:ingest
	// JWT the sentinel's SSH session shipper uses to append to that
	// backend's audit chain.
	sentinelTokenKindAuditIngest sentinelTokenKind = "audit-ingest"
)

func (k *sentinelTokenKind) String() string { return string(*k) }

func (k *sentinelTokenKind) Set(v string) error {
	switch sentinelTokenKind(v) {
	case sentinelTokenKindTunnelJoin, sentinelTokenKindAuditIngest:
		*k = sentinelTokenKind(v)
		return nil
	}
	return fmt.Errorf("invalid kind %q (want %q or %q)", v, sentinelTokenKindTunnelJoin, sentinelTokenKindAuditIngest)
