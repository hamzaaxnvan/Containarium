package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
)

func runOrgCtx(scopes string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		auth.MDKeyUsername, "oss-primary",
		auth.MDKeyRoles, auth.RoleAdmin,
