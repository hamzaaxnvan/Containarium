package cluster

import (
	"testing"

	"github.com/lxc/incus/v7/shared/api"
)

// The Incus config a cluster node is created from. Pure, so the parts
// Incus validates before it will create anything are testable without a
// real host — which is where #1435 hid: a root disk carrying only a
// size is rejected with `Disk entry is missing the required "source" or
// "path" property`, so no node of either isolation class was ever
// created.
func TestNodeContainerConfig(t *testing.T) {
	spec := NodeSpec{Name: "t-k8s-c-small-1", CPU: "2", Memory: "4GB", Disk: "40GB"}

	tests := []struct {
		name         string
		spec         NodeSpec
		isolation    Isolation
		wantType     api.InstanceType
		wantNesting  bool
		wantDisk     bool
		wantDiskSize string
	}{
		{
			name:         "vm node",
			spec:         spec,
			isolation:    IsolationVM,
			wantType:     api.InstanceTypeVM,
			wantDisk:     true,
			wantDiskSize: "40GB",
		},
		{
