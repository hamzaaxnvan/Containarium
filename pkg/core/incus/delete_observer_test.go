package incus

import (
	"errors"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// fakeDeleteServer serves one instance and deletes it.
type fakeDeleteServer struct {
	incusclient.InstanceServer
	inst      *api.Instance
	deleteErr error
	deleted   []string
}

func (f *fakeDeleteServer) GetInstance(string) (*api.Instance, string, error) {
	if f.inst == nil {
		return nil, "", api.StatusErrorf(404, "Instance not found")
	}
	cp := *f.inst
	return &cp, "etag", nil
}

func (f *fakeDeleteServer) DeleteInstance(name string) (incusclient.Operation, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	return fakeOp{}, nil
}

// TestDeleteContainer_TellsTheDeleteObservers (#2835 review, item 3): every
// delete that goes through the Incus client, whichever server path issued it
// (DeleteContainer, the cloud actuator, sandboxes), tells the registered
// observers which box went and which instance it was, so the process
// responder can close that instance's action and only that one.
func TestDeleteContainer_TellsTheDeleteObservers(t *testing.T) {
	type seen struct{ name, id string }
	var got []seen
	remove := OnContainerDeleted(func(name, instanceID string) { got = append(got, seen{name, instanceID}) })
