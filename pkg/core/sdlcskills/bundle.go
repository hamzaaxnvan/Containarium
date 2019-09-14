package sdlcskills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// Bundle is a verified, in-memory view of sdlc-skills.tar.gz, the release
// asset scripts/build-sdlc-skills-bundle.sh produces and the boxes install.
// A daemon that composes a hosted skill's prompt from it (#2488) therefore
// runs the same pinned bytes the boxes do.
type Bundle struct {
	pin   string
	files map[string]string
}

// Pin names the content, e.g. "FootprintAI/open-sdlc-skills@<sha12>", for
// stamping into whatever is derived from it.
func (b *Bundle) Pin() string { return b.pin }

// Section returns one bold section of one skill's body (see ExtractSection),
// or false if the skill or the section is absent. This is what a hosted skill
// composes its prompt from.
func (b *Bundle) Section(skill, heading string) (string, bool) {
	body, ok := b.SkillBody(skill)
	if !ok {
		return "", false
	}
	return ExtractSection(body, heading)
}

// maxBundleBytes bounds what LoadBundle will read: the real asset is ~100 KB
// of markdown, so a multi-megabyte file at that path is not it.
const maxBundleBytes = 8 << 20

// LoadBundle reads the tarball at path and verifies it against the pin this
// binary was built with. Anything but the exact pinned bytes is an error; the
// caller falls back to its inline prompt rather than trusting other content.
func LoadBundle(filePath string) (*Bundle, error) {
	lock, err := Embedded()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filePath) // #nosec G304 -- operator-configured bundle path, verified by digest below
	if err != nil {
		return nil, fmt.Errorf("sdlcskills: open bundle: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("sdlcskills: read bundle: %w", err)
	}
	if len(data) > maxBundleBytes {
		return nil, fmt.Errorf("sdlcskills: bundle at %s exceeds %d bytes; not the pinned asset", filePath, maxBundleBytes)
	}
	return LoadBundleBytes(data, lock)
}

// LoadBundleBytes verifies data against lock and parses it. Exposed so a
// caller with its own lock (tests, tooling) can use the same verification.
func LoadBundleBytes(data []byte, lock Lock) (*Bundle, error) {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != lock.SHA256 {
		return nil, fmt.Errorf("sdlcskills: bundle digest %s does not match the pinned digest %s", got[:12], lock.SHA256[:12])
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("sdlcskills: bundle is not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	files := map[string]string{}
	tr := tar.NewReader(gz)
	for {
