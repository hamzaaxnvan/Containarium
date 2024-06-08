package sshauthz

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The self-host decider table (#2517, done-when 1) is shared by the unit
// run (in-memory key store) and the integration run (real Postgres), so
// both halves answer the same question: given these local facts and this
// registry row, what is the decision and the reason?

var caseNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type fakeBoxes struct {
	names map[string]bool
	err   error
}

func (f fakeBoxes) ExistingContainerNames() (map[string]bool, error) { return f.names, f.err }

type fakeCollaborators struct {
	rows map[string]bool // "<container>/<collaborator>"
	err  error
}

func (f fakeCollaborators) CollaboratorExists(_ context.Context, containerName, collaboratorUsername string) (bool, error) {
	return f.rows[containerName+"/"+collaboratorUsername], f.err
}

// testKey returns a fresh ed25519 authorized_keys line.
func testKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(pk))
}

// writeHome creates <root>/<user>/.ssh/authorized_keys with content.
func writeHome(t *testing.T, root, user, content string) {
	t.Helper()
	dir := filepath.Join(root, user, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type deciderCase struct {
	name string

	login         string
	homeAccount   bool // jump account present with a non-empty authorized_keys
	boxes         map[string]bool
	boxesErr      bool
	collaborators map[string]bool

	registered bool          // a registry row exists for the offered key
	revoked    bool          // that row is revoked
	age        time.Duration // caseNow - created_at
	keyType    pb.SSHKeyType // stored key type (the offered key is ed25519 on the wire either way)
	maxKeyAge  time.Duration
	noCred     bool // request carries a certificate instead of a public key

	wantDecision pb.SSHAuthzDecision
	wantReason   pb.SSHDenyReason
	wantUserID   string
}

var (
	allow = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW
	deny  = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY
	none  = pb.SSHDenyReason_SSH_DENY_REASON_UNSPECIFIED

	noAccess   = pb.SSHDenyReason_SSH_DENY_REASON_NO_ACCESS
	unknownKey = pb.SSHDenyReason_SSH_DENY_REASON_UNKNOWN_KEY
	expired    = pb.SSHDenyReason_SSH_DENY_REASON_KEY_EXPIRED

	ed = pb.SSHKeyType_SSH_KEY_TYPE_ED25519
	sk = pb.SSHKeyType_SSH_KEY_TYPE_SK_ED25519
)

var aliceBox = map[string]bool{"alice-container": true}

func deciderCases() []deciderCase {
	day := 24 * time.Hour
	return []deciderCase{
		// Local facts.
		{name: "owner, registered key → allow", login: "alice", homeAccount: true, boxes: aliceBox,
			registered: true, keyType: ed, wantDecision: allow, wantReason: none, wantUserID: "alice"},
		{name: "no jump account → no_access", login: "alice", homeAccount: false, boxes: aliceBox,
			registered: true, keyType: ed, wantDecision: deny, wantReason: noAccess},
		{name: "orphan: account but box deleted → no_access", login: "alice", homeAccount: true, boxes: map[string]bool{},
			registered: true, keyType: ed, wantDecision: deny, wantReason: noAccess},
		{name: "box snapshot error fails closed → no_access", login: "alice", homeAccount: true, boxesErr: true,
			registered: true, keyType: ed, wantDecision: deny, wantReason: noAccess},
		{name: "unsafe login → no_access", login: "../alice", homeAccount: false, boxes: aliceBox,
