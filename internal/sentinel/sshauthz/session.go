package sshauthz

import (
	"fmt"
	"net/url"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	"github.com/tg123/sshpiper/libplugin"
)

// Bounds on decisions kept for connections whose pipe has not started, such
// as a connection that was routed on a publickey query and then dropped.
const (
	maxPending = 10000
	pendingTTL = 10 * time.Minute
)

type pendingConn struct {
	meta map[string]string
	at   time.Time
}

// pass handles an offer this plugin does not refuse. Audit-only mode defers it
// with the decision metadata. Enforce mode routes it as the yaml plugin would
