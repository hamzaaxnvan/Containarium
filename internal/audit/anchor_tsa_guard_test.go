package audit

import (
	"context"
	"crypto"
	"crypto/sha512"
	"encoding/asn1"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
)

// #2728 review of #3150: guards on stored token files and on the token
// shape that other tests did not pin.

// The gate must run on the verify-anchor path too: a hostile .tsr on disk is
// a token-invalid finding carrying ErrTSANotStrictDER.
func TestVerifyAnchorsAgainstTSA_HostileTokenFileHitsTheDERGate(t *testing.T) {
	for name, in := range hostileDER() {
		t.Run(name, func(t *testing.T) {
			f := newTSAVerifyFixture(t, 30)
			f.anchor(10)
			if err := os.WriteFile(filepath.Join(f.sink.TokenDir(), tsaTokenName(10)), in, 0o600); err != nil {
				t.Fatal(err)
			}
			r := f.verify(f.chain)
			if len(r.Failures) == 0 || r.Failures[0].Kind != TSAFindingTokenInvalid || !errors.Is(r.Failures[0].Err, ErrTSANotStrictDER) {
				t.Fatalf("failures = %+v, want token-invalid with ErrTSANotStrictDER", r.Failures)
			}
		})
	}
}

// allocDuring reports the bytes allocated while f runs.
func allocDuring(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// maxReadAlloc is far above one capped read and far below the 1 GiB files.
const maxReadAlloc = 64 << 20

// writeSparse makes a file of size bytes without writing them.
func writeSparse(t *testing.T, path string, size int64) {
	t.Helper()
	fh, err := os.Create(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := fh.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadTokenFile_RefusesOversizedAndNonRegular(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.tsr")
