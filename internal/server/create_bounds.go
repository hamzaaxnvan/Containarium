package server

import (
	"github.com/footprintai/containarium/pkg/core/stacks"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 3.3 / 3.4 — bounds on unbounded proto fields.
//
// CreateContainerRequest's `ssh_keys` (repeated string),
// `stack_parameters` (map<string,string>) and `labels`
// (map<string,string>) had no wire-level caps. A caller could
// submit a million-entry array or a single key/value of megabytes,
// blowing through memory and downstream config-store limits. The
// proto contract isn't changed (would require a regenerate + a
// breaking-change announcement); the bounds live here, at the
// server's API boundary.

const (
	// MaxSSHKeys caps the number of ssh_keys per CreateContainer.
	// A realistic user has 1–5; 32 is a generous power-of-two ceiling.
	MaxSSHKeys = 32

	// MaxSSHKeyLength caps each individual key string. Real
	// ssh-ed25519 keys are ~80 chars; ssh-rsa 4096 ed25519 are
	// ~720. 8 KiB is well above any legitimate key and still small
	// enough that 32 keys total is bounded.
	MaxSSHKeyLength = 8 * 1024

	// MaxStackParameters caps the stack_parameters map cardinality.
	MaxStackParameters = 64

	// MaxStackParameterValueLen caps each stack-parameter value.
	// Stack parameters become container env vars; the kernel's
	// ARG_MAX is 128 KiB on Linux, so 4 KiB per value keeps even a
	// fully-populated map well under the limit.
	MaxStackParameterValueLen = 4 * 1024

	// MaxStackParameterKeyLen caps each stack-parameter key.
	MaxStackParameterKeyLen = 256

	// MaxStackIDLength caps the stack ID, which is echoed in the rejection
	// for an unknown one. Catalog IDs are short identifiers.
	MaxStackIDLength = 64

