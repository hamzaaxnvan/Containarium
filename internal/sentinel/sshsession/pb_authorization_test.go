package sshsession

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/proto"
)

// Uses the real enum contract introduced by #2516 (PR #2534).
func TestAuthorization_ProtoRoundTrip(t *testing.T) {
	for number, name := range pb.SSHDenyReason_name {
