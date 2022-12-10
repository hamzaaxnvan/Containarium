package incus

import (
	"log"
	"sync"
)

// deleteObservers are told of every container this process deletes through
// Client.DeleteContainer (#2835), whichever server path issued the delete:
// the DeleteContainer RPC, the cloud actuator, sandboxes. The process
// responder uses it to close an action left open on a box that no longer
// exists. Process-wide on purpose: every path that deletes an instance goes
// through a Client, and the daemon builds several.
var deleteObservers struct {
	mu   sync.RWMutex
	next int
