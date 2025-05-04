package server

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/secrets"
	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

// #1630 — SetTenantKMSKey's authz is deliberately NOT
// AuthorizeSecretTenant's self-access-exempt shape: admin role +
// explicit secrets:write scope are required unconditionally, even when
// the caller's own subject equals the requested username. These tests
// pin that directly, mirroring internal/auth/secrets_authz_test.go's
// context-construction convention (metadata.NewIncomingContext with the
// same MD keys) since SetTenantKMSKey doesn't route through
// AuthorizeSecretTenant at all.

func kmsKeyTestCtx(username, roles, scopes string) context.Context {
