package sentinel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultTunnelTokenStorePath is where dynamically-registered tunnel
// tokens (see TunnelTokenRegisterHandler) are persisted so a sentinel
// restart doesn't silently forget them (#936). TokenPolicy itself is a
// pure in-memory value with no memory across process restarts; a token
// registered via POST /sentinel/tunnel-tokens minutes or days before a
// restart would otherwise vanish, permanently locking out every host
// whose next handshake happens to land after that restart.
const DefaultTunnelTokenStorePath = "/etc/containarium/tunnel-tokens.json" // #nosec G101 -- a file PATH, not a credential

// TunnelTokenEntry is one persisted dynamic registration.
type TunnelTokenEntry struct {
	Token string `json:"token"`
	Pools []Pool `json:"pools"`
}

// LoadTunnelTokenStore reads previously-persisted dynamic registrations
// from path. A missing file is not an error — it means either a fresh
// sentinel or one that has never served a dynamic registration yet;
