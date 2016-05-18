package sentinel

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// Event-driven key resync (cloud #971).
//
// The sentinel learns about a box's SSH key by polling each backend's
// /authorized-keys on a ticker (RunSyncLoop, default 2m). A box created just
// after a tick is therefore RUNNING — and correct in every other respect —
// while sshpiper has no pipe for it, so SSH answers
// `Permission denied (publickey)` until the next tick. That is a 0–120s
// window, mean ~60s, which is exactly the ~50s users measured.
//
// This endpoint closes the window without shortening the tick: the daemon
// tells the sentinel "my key set changed, pull it now" whenever it installs
// or removes a host-side authorized_keys entry. The ticker stays as the
// convergence backstop for anything the notification missed (daemon crash
// mid-create, sentinel restart, a notification lost in flight).

// KeyResyncRequest is the JSON body POSTed to KeyResyncHandler by a daemon
// whose host-side authorized_keys set just changed.
type KeyResyncRequest struct {
	// BackendID identifies which backend to re-pull. Empty falls back to
	// resolving the backend by the request's source IP, which covers a
	// daemon that hasn't been told its own backend ID.
	BackendID string `json:"backend_id,omitempty"`

	// Reason is free-form and logged only (e.g. "create_container
	// cld-1a2b3c") so an operator reading sentinel logs can tell an
	// event-driven sync from a periodic one.
	Reason string `json:"reason,omitempty"`
}

