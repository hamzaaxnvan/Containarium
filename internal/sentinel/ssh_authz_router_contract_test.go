package sentinel

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/sentinel/sshauthz"
	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
)

// Contract: the enforce-mode router reads the config.yaml renderSSHPiperConfig
// actually writes. The router refuses fields outside the subset it models, so
// a renderer change it cannot read fails here, not as refused logins.
func TestSSHAuthzRouterReadsRenderedConfig(t *testing.T) {
	newKey := func() ssh.Signer {
		t.Helper()
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		return signer
	}
	alice, bob, ca := newKey(), newKey(), newKey()

	cert := &ssh.Certificate{Key: newKey().PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{"alice"}, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
