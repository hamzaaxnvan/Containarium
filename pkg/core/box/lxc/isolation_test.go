package lxc

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestStatusFromInfo_Isolation(t *testing.T) {
	tests := []struct {
		instanceType string
		want         pb.IsolationType
	}{
		{"virtual-machine", pb.IsolationType_ISOLATION_TYPE_VM},
		{"container", pb.IsolationType_ISOLATION_TYPE_CONTAINER},
		{"", pb.IsolationType_ISOLATION_TYPE_UNSPECIFIED},
