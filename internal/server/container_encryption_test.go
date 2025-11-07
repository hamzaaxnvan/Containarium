package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// AC: `encrypted=true` with no KeyProvider wired fails with
// FAILED_PRECONDITION — never a silent fall back to plaintext.
//
// This is the criterion the whole flag exists for. A create that accepts
// "encrypt this" and quietly provisions an unencrypted dataset hands the
// caller a guarantee they do not have, and they find out during an audit
// rather than at create time.
func TestValidateEncryptionRequiresAKeyProvider(t *testing.T) {
	err := validateEncryption(&pb.CreateContainerRequest{Encrypted: true}, nil)
	if err == nil {
		t.Fatal("encrypted=true was accepted with no KeyProvider — the request must fail rather than silently produce plaintext")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", got)
	}
	if !strings.Contains(err.Error(), "KeyProvider") {
		t.Errorf("error should name the missing dependency, got %q", err)
	}
}

// With a provider wired, an encrypted create is accepted.
func TestValidateEncryptionAcceptedWithAProvider(t *testing.T) {
	provider, err := zfskey.NewFileKeyProvider(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileKeyProvider: %v", err)
	}
	if err := validateEncryption(&pb.CreateContainerRequest{Encrypted: true}, provider); err != nil {
		t.Errorf("encrypted create rejected despite a wired provider: %v", err)
	}
}

// AC: the default path is unchanged. encrypted=false must not require a
// provider — the overwhelming majority of creates are unencrypted and
// must not start failing on a daemon with no key custody configured.
func TestValidateEncryptionIgnoredWhenNotRequested(t *testing.T) {
	if err := validateEncryption(&pb.CreateContainerRequest{}, nil); err != nil {
		t.Errorf("a plain create must not need a KeyProvider: %v", err)
	}
	if err := validateEncryption(&pb.CreateContainerRequest{Encrypted: false}, nil); err != nil {
		t.Errorf("encrypted=false must not need a KeyProvider: %v", err)
	}
