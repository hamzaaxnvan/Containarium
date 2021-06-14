package server

import (
	"context"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
	"time"
)

func nativeAdmin() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "operator", []string{auth.RoleAdmin}, []string{auth.ScopeContainersRead, auth.ScopeContainersWrite})
}
func nativeUser() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "workload", []string{"user"}, []string{auth.ScopeContainersRead, auth.ScopeContainersWrite})
}

func TestNativeTenantAuthorizationAndPlacement(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		admin, local, tenancyOn bool
		req                     *pb.CreateContainerRequest
		want                    codes.Code
		key                     string
	}{
		{"admin explicit", true, true, false, &pb.CreateContainerRequest{NetworkTenant: "team-a"}, codes.OK, "team-a"},
		{"admin omitted", true, true, false, &pb.CreateContainerRequest{}, codes.OK, ""},
		{"user omitted legacy path when switch off", false, true, false, &pb.CreateContainerRequest{}, codes.OK, ""},
		{"user omitted reserved when switch on", false, true, true, &pb.CreateContainerRequest{}, codes.OK, "account:workload"},
		{"user omitted on non-local host legacy", false, false, true, &pb.CreateContainerRequest{}, codes.OK, ""},
		{"user shared forbidden", false, true, false, &pb.CreateContainerRequest{NetworkTenant: "team-a"}, codes.PermissionDenied, ""},
