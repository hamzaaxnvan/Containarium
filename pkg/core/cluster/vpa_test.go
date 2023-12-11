package cluster

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"regexp"
	"strings"
	"testing"
)

// TestVPAPin is the golden pin for what runs inside tenant clusters:
// bundle hash, version, and every image digest-pinned. Re-vendoring
// means changing the bundle AND this test, on purpose.
func TestVPAPin(t *testing.T) {
	m, err := VPAManifests()
	if err != nil {
		t.Fatalf("VPAManifests: %v", err)
	}
	sum := sha256.Sum256(m)
	if hex.EncodeToString(sum[:]) != VPAManifestSHA256 {
		t.Fatal("embedded bundle does not match its pin")
	}
	if VPAVersion != "1.7.1" {
		t.Fatalf("VPAVersion pin changed to %q — re-verify images and manifests against the upstream tag", VPAVersion)
	}

	text := string(m)
	// Every component image is digest-pinned; no floating tags.
	imageRE := regexp.MustCompile(`image:\s*(\S+)`)
	images := imageRE.FindAllStringSubmatch(text, -1)
	if len(images) != 3 {
		t.Fatalf("expected 3 component images, found %d", len(images))
	}
	for _, im := range images {
		if !strings.Contains(im[1], "@sha256:") {
			t.Fatalf("image %q is not digest-pinned", im[1])
		}
	}
	// The bundle carries the three components + the webhook service,
	// and deliberately NOT the TLS secret (generated per cluster).
	for _, must := range []string{"vpa-recommender", "vpa-updater", "vpa-admission-controller", "kind: Service"} {
		if !strings.Contains(text, must) {
			t.Fatalf("bundle missing %q", must)
		}
	}
	if strings.Contains(text, "kind: Secret") {
		t.Fatal("the bundle must not contain a shared TLS secret — it is generated per cluster")
	}
}

func TestGenerateVPAWebhookSecret(t *testing.T) {
	secret, err := GenerateVPAWebhookSecret()
	if err != nil {
		t.Fatal(err)
	}
	text := string(secret)
