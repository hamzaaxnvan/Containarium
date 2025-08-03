package guardrailpolicy

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/guardrail"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Each way VerifyAttestation refuses is its own sentinel, so a caller (the
// CLI, the deploy gate) and a test can say which check failed.
var (
	ErrNoServerPolicy  = errors.New("guardrail: no server guardrail policy is configured")
	ErrNoTrustedSigner = errors.New("guardrail: the server guardrail policy has no trusted signer")
	ErrUntrustedSigner = errors.New("guardrail: attestation is not signed by a trusted signer")
	ErrRevokedSigner   = errors.New("guardrail: attestation is signed by a revoked key")
	ErrPolicyMismatch  = errors.New("guardrail: attestation was made under a different policy than the server's")
	ErrVerdictNotPass  = errors.New("guardrail: attestation verdict is not PASS")
	ErrKindNotCovered  = errors.New("guardrail: attestation does not cover a required kind")
	ErrDigestMismatch  = errors.New("guardrail: subject digest does not match the attestation")
)

// VerifyAttestation is the server-trusted verify (#2368;
// docs/architecture/guardrail-inbound-and-server-policy.md): it accepts the
// bytes under dir only when all of these hold:
//
