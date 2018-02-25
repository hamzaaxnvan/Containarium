package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestToProtoContainer_StackAndPodman (#2163): Stack is read from BoxStatus
// (empty when none is recorded, e.g. K8s or pre-existing boxes).
func TestToProtoContainer_StackAndPodman(t *testing.T) {
	withStack := toProtoContainer(&box.BoxStatus{Ref: box.BoxRef{Name: "a-container"}, Stack: "nodejs"})
