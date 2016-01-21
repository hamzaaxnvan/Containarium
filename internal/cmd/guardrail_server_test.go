package cmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	"github.com/footprintai/containarium/internal/mtls"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeServerFlag names a server explicitly, the only way apply/verify are
// allowed to contact one (#2368). The fake API never dials it.
var fakeServerFlag = []string{"--server", "fake.invalid:50051"}

// runGuardrailAt runs a guardrail verb with an explicitly named server.
func runGuardrailAt(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runGuardrail(t, append(args, fakeServerFlag...)...)
}

// piiOnlyPolicy differs from guardrail.DefaultPolicy (it ALLOWs secrets), so
// an attestation made under the default policy has a different policy_hash.
func piiOnlyPolicy() *pb.GuardrailPolicy {
	return &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{
		{Kind: pb.GuardrailKind_GUARDRAIL_KIND_PII, Action: pb.GuardrailAction_GUARDRAIL_ACTION_REDACT},
		{Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Action: pb.GuardrailAction_GUARDRAIL_ACTION_ALLOW},
	}}
}

// guardrailWork is a raw dataset and a signing key pair in a temp dir.
type guardrailWork struct {
	in, out, keys string
}

func newGuardrailWork(t *testing.T) guardrailWork {
	t.Helper()
	work := t.TempDir()
	w := guardrailWork{in: filepath.Join(work, "raw"), out: filepath.Join(work, "clean"), keys: filepath.Join(work, "tenant")}
	must(t, os.MkdirAll(w.in, 0o755))
	must(t, os.WriteFile(filepath.Join(w.in, "note.txt"), []byte("Contact ann@example.com please.\n"), 0o644))
	if o, err := runGuardrail(t, "keygen", "--out", w.keys); err != nil {
		t.Fatalf("keygen: %v\n%s", err, o)
	}
	return w
}

func (w guardrailWork) attestationFile() string { return w.out + ".attestation.json" }

// written lists every file apply may write; a refused apply writes none.
func (w guardrailWork) written() []string {
	return []string{w.out, w.attestationFile(), w.out + ".vault.json", w.out + ".redaction.key"}
}

func (w guardrailWork) assertNothingWritten(t *testing.T) {
	t.Helper()
	for _, p := range w.written() {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after a refused apply", p)
		}
	}
}

func (w guardrailWork) readAttestation(t *testing.T) *pb.GuardrailAttestation {
	t.Helper()
	b, err := os.ReadFile(w.attestationFile())
	if err != nil {
		t.Fatal(err)
	}
	att := &pb.GuardrailAttestation{}
	must(t, protojson.Unmarshal(b, att))
	return att
}

// setServerPolicy stores policy on the fake daemon, trusting the key pairs
// under the given prefixes.
func setServerPolicy(t *testing.T, fake *fakeGuardrailPolicyAPI, policy *pb.GuardrailPolicy, signerPrefixes ...string) string {
	t.Helper()
	var signers []*pb.GuardrailTrustedSigner
	for _, p := range signerPrefixes {
		pub, err := guardrail.LoadPublicKey(p + ".pub")
		must(t, err)
		signers = append(signers, &pb.GuardrailTrustedSigner{KeyId: guardrail.KeyID(pub), PublicKey: pub, Label: filepath.Base(p)})
	}
	if _, err := fake.store.Set(context.Background(), &pb.SetGuardrailPolicyRequest{Policy: policy, TrustedSigners: signers}, "ops"); err != nil {
		t.Fatal(err)
	}
	h, err := guardrail.PolicyHash(policy)
	must(t, err)
	return h
}

func writeGuardrailPolicyJSON(t *testing.T, policy *pb.GuardrailPolicy) string {
	t.Helper()
	b, err := protojson.Marshal(policy)
	must(t, err)
	return writePolicyFile(t, string(b))
}

// TestGuardrailApply_ServerPolicy: with an explicitly named server that
// holds a policy, apply runs under the server's policy; --policy is
// accepted only when it hashes to the server's.
func TestGuardrailApply_ServerPolicy(t *testing.T) {
	t.Run("no --policy uses the server's", func(t *testing.T) {
