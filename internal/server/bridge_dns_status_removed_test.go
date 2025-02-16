package server

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2733 (slice B of #2263): the bridge-DNS status surface is gone from the
// contract. The kernel hairpin rule (#2732) replaces the record it reported on.
func TestBridgeDNSStatusRemovedFromContract(t *testing.T) {
	svc := pb.File_containarium_v1_service_proto.Services().ByName("ContainerService")
	if svc == nil {
		t.Fatal("ContainerService not found in the generated descriptor; test harness broken")
