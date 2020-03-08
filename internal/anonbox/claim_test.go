package anonbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// claimHarness: a harness with claims enabled (per-box secret derived from
// the name, like DeriveSharedSecret would) and one box already ensured.
func claimHarness(t *testing.T, base string) (*harness, string, string) {
	t.Helper()
	h := newHarness(t, func(c *Config) {
		c.ClaimSecret = func(box string) string { return "secret-for-" + box }
		c.ClaimURLBase = base
	})
	key, fp := testKey(t)
	res, err := h.m.Ensure(context.Background(), EnsureRequest{Fingerprint: fp, PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	claimFile := fileNamed(t, h.boxes.files[res.BoxName], ClaimURLPath).content
	return h, res.BoxName, strings.TrimSpace(claimFile)
}

func tokenFromClaimFile(t *testing.T, content string) string {
	t.Helper()
	if i := strings.Index(content, "?token="); i >= 0 {
		return content[i+len("?token="):]
	}
	return content
}

func TestEnsure_WritesClaimURLWithMintedToken(t *testing.T) {
	h, boxName, content := claimHarness(t, "https://cloud.example.test/claim/")
	if !strings.HasPrefix(content, "https://cloud.example.test/claim?token=v1."+boxName+".") {
