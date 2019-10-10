package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/credentials"
)

func seedServerCreds(t *testing.T, servers ...string) {
	t.Helper()
	path, err := credentials.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cf := credentials.NewCredentialsFile()
	for _, s := range servers {
		cf.Set(s, credentials.ServerCreds{Token: "tok-" + s})
	}
	if err := credentials.Save(path, cf); err != nil {
		t.Fatal(err)
	}
}

// #2238: `--server host` must resolve to the https URL login stored, and
// find that server's token.
func TestCanonicalServerAddr_SchemelessResolvesToStoredURL(t *testing.T) {
	withTempHome(t)
	seedServerCreds(t, "https://cluster.example.com")

	got := canonicalServerAddr("cluster.example.com")
	if got != "https://cluster.example.com" {
		t.Fatalf("canonicalServerAddr = %q, want the stored https URL", got)
	}
	// Compare fingerprints, never the tokens: a failure must not print one (#2922).
	want := "tok-https://cluster.example.com"
	if tok := resolveAuthToken(got); tok != want {
		t.Fatalf("token fingerprint = %s, want %s (the seeded token)", tokenFingerprint(tok), tokenFingerprint(want))
	}
}

func TestCanonicalServerAddr_LeavesOtherInputsAlone(t *testing.T) {
