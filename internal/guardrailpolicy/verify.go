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
//   - server is a configured policy;
//   - the attestation's key_id is not revoked (#2729): whatever its
//     attested_at says, since the signer sets that time;
//   - the policy has at least one trusted signer;
//   - the attestation's key_id names one of server.trusted_signers, and the
//     signature verifies against THAT signer's key (never a key the caller
//     supplies: a caller-supplied key would let anyone sign their own input);
//   - policy_hash equals the hash of server.policy (the inner rules only);
//   - the verdict is PASS;
//   - kinds_scanned covers every kind the server policy scans for, plus
//     require;
//   - the digest recomputed over dir equals the attested subject_sha256.
//
// It returns the trusted signer that matched. Both `guardrail verify` and the
// DeployRecipe gate call it, so the two cannot disagree on what "verified"
// means.
func VerifyAttestation(att *pb.GuardrailAttestation, server *pb.ServerGuardrailPolicy, dir string, require []pb.GuardrailKind) (*pb.GuardrailTrustedSigner, error) {
	signer, err := VerifyClaims(att, server, require)
	if err != nil {
		return nil, err
	}
	got, err := guardrail.SubjectDigest(dir)
	if err != nil {
		return nil, err
	}
	if got != att.GetSubjectSha256() {
		return nil, fmt.Errorf("%w (got %s, attested %s): the bytes changed after attestation", ErrDigestMismatch, shortHex(got), shortHex(att.GetSubjectSha256()))
	}
	return signer, nil
}

