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
// and returns the upstream itself: sshpiperd's chain only moves forward within
// a connection, so a deferral would let yaml alone judge every later offer on
// that connection (#2629). sshpiperd copies no metadata when a plugin returns
// an upstream, so the decision, credential and routed target are kept here,
// by connection, for this plugin's own session records. The SSH server
// re-runs this callback for the key that finally signs unless it was the last
// key checked, so the last routed offer is the one that authenticated.
func (p *Plugin) pass(conn libplugin.ConnMetadata, wire []byte, meta map[string]string) (*libplugin.Upstream, error) {
	if !p.cfg.Enforce {
		return deferWith(meta)
	}
	up, err := p.cfg.Router.Route(conn, wire)
	if err != nil {
		p.cfg.Logf("[ssh-authz] login=%q: not routed: %v", conn.User(), err)
		return nil, fmt.Errorf("ssh authz: login %q not routed: %w", conn.User(), err)
	}
	for k, v := range sshsession.CredentialMeta(wire) {
		meta[k] = v
	}
	// Every session admitted in enforce mode is recorded enforced, including
	// one routed without a daemon decision (a certificate): the decision
	// field, not this marker, says whether the daemon was asked.
	meta[sshsession.MetaEnforced] = "true"
	meta[sshsession.MetaTarget] = upstreamTarget(up)
	p.remember(conn.UniqueID(), meta)
	return up, nil
