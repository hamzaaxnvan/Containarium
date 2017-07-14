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
	fns  map[int]func(name, instanceID string)
}

// OnContainerDeleted registers fn to be called after each successful
// Client.DeleteContainer with the container's name and its Incus instance id
// (volatile.uuid, read just before the delete; "" when it could not be
// read). fn runs on the deleting goroutine, so it must not block. The
// returned func removes it.
func OnContainerDeleted(fn func(name, instanceID string)) (remove func()) {
	deleteObservers.mu.Lock()
	defer deleteObservers.mu.Unlock()
	if deleteObservers.fns == nil {
		deleteObservers.fns = map[int]func(string, string){}
	}
	id := deleteObservers.next
	deleteObservers.next++
	deleteObservers.fns[id] = fn
