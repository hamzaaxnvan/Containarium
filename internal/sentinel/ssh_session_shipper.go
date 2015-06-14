package sentinel

import (
	"fmt"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
)

// #2415 — the pieces of the SSH session shipper that live in the sentinel
// process: its backend pool (id -> address), keysync's login -> backend
// routing, and the audit-ingest token file. internal/cmd assembles them with
// the typed client and runs the loop (see startSSHSessionShipper).

// FileAuditIngestTokens is a sshsession.TokenSource over the audit-ingest
// token file. It re-reads the file on every call, so a token registered or
// rotated with `sentinel register-token --kind audit-ingest` takes effect on
// the next pass with no restart.
type FileAuditIngestTokens struct {
	Path string
}

// TokenFor returns backendID's registered token.
func (f FileAuditIngestTokens) TokenFor(backendID string) (string, bool, error) {
	entries, err := LoadAuditIngestTokenStore(f.Path)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.BackendID == backendID {
			return e.Token, true, nil
		}
	}
	return "", false, nil
}

// Backends lists every backend that has a registered token.
func (f FileAuditIngestTokens) Backends() ([]string, error) {
	entries, err := LoadAuditIngestTokenStore(f.Path)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.BackendID
	}
	return out, nil
}

// ShipperBackendURL is the REST base URL of a backend's daemon, the same
