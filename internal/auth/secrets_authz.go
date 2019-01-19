package auth

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthorizeSecretTenant is AuthorizeTenant for the secret RPCs, with one extra
// requirement on the cross-tenant path.
//
// Why this exists rather than reusing AuthorizeTenant. Two behaviors that are
// each reasonable on their own compose into an unintended one:
//
