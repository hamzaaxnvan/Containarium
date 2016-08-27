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
