package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CreateContainerRequest.encrypted was accepted and never read (#1199).
//
// The proto says the request "fails with FAILED_PRECONDITION; it never falls
// back to plaintext, because a create that silently ignores this flag would
// hand the caller an encryption guarantee they do not have". The CLI help
// says the same. The server did neither: it created an ordinary container on
// the pool-wide key and reported success.
//
// That is worse than an unimplemented feature. An operator who runs
// `containarium create --encrypted` gets a container they believe is
// tenant-encrypted, and nothing anywhere tells them otherwise.
//
// #1341 UPDATE. The blanket refusal this test was written against is gone,
// because the reason for it is gone: the create path now does provision
// encrypted storage. What survives — and what this test now asserts — is the
// refusal that actually keeps the promise: no KeyProvider, no encrypted
// create. Every OSS daemon is in that state until an operator configures one
// (#1342), so the externally visible behaviour here is unchanged.
//
// The assertion moved off the phrase "not available", which no longer appears
// anywhere, and onto the guarantee: FAILED_PRECONDITION, and a message that
// identifies a daemon capability gap rather than a caller mistake.
func TestCreateContainer_RefusesEncryptedRatherThanIgnoringIt(t *testing.T) {
	s := &ContainerServer{}

	_, err := s.CreateContainer(tenantWithScopes("alice", auth.ScopeContainersWrite), &pb.CreateContainerRequest{
		Username:  "alice",
		Encrypted: true,
	})
	if err == nil {
