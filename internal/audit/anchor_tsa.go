package audit

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
)

// #2728: an RFC 3161 timestamp-authority (TSA) anchor sink.
//
// The GCS sink (#2880) needs an object store with a retention lock. Where
// there is none, a TSA gives each anchor an external, signed proof of time:
// the TSA signs "this SHA-256 existed at genTime", and anyone holding the
// TSA's root certificate can check that signature later without trusting
// this host. The token does not hold the anchor itself, only its hash, so it
// is stored next to the local anchor file, one DER file per checkpoint:
//
//	<anchor-path>.tsa/<checkpoint, zero-padded to 20 digits>.tsr
//
// What the token proves, and what it does not, is in the operator runbook
// ("Anchoring to a timestamp authority"). In short: a host-root attacker who
// edits a stamped line in the local file is caught (the hash no longer
// matches), but one who deletes the token files, or the whole directory, is
// not caught by the tokens alone: the check can only warn that a checkpoint
// has no token.
//
// Decisions Q3-Q7 on #2728: the operator configures the TSA URL and trust
// bundle (no built-in TSA); requests go over net/http with a 60 s timeout;
// a failed stamp leaves the local record, stores nothing, and is retried on
// the next tick; every token is verified against the trust bundle before it
// is stored; a random 64-bit nonce is sent and checked; certReq is set so the
// token carries the TSA certificate; the chain is checked at genTime; no
// CRL/OCSP revocation check.

// tsaCallTimeout bounds one TSA request, like gcloudCallTimeout.
const tsaCallTimeout = 60 * time.Second

// maxTSAResponseBytes bounds a TSA reply. A token with its certificate chain
// is a few kilobytes.
const maxTSAResponseBytes = 256 << 10

// maxTSADERDepth bounds ASN.1 nesting in a token. A CMS SignedData with a
// certificate chain nests well under 20 deep.
const maxTSADERDepth = 32

var (
	// ErrTSATokenInvalid: the token or reply is malformed, its signature does
	// not verify, or it carries no TSA certificate.
	ErrTSATokenInvalid = errors.New("audit: timestamp token is invalid")
	// ErrTSAUntrustedSigner: the token's signing certificate does not chain to
	// the trust bundle at the token's genTime, or is not a timestamping
	// certificate.
	ErrTSAUntrustedSigner = errors.New("audit: timestamp token is not signed by a TSA in the trust bundle")
	// ErrTSAImprintMismatch: the token is valid but covers a different hash
	// than the anchor record it is checked against.
	ErrTSAImprintMismatch = errors.New("audit: timestamp token covers a different anchor record")
	// ErrTSANonceMismatch: the reply's nonce is not the one sent.
	ErrTSANonceMismatch = errors.New("audit: timestamp reply nonce does not match the request")

	// ErrTSANotStrictDER: a token or reply failed the strict DER gate that
	// runs before any pkcs7 parse. Always joined with ErrTSATokenInvalid.
	ErrTSANotStrictDER = errors.New("audit: timestamp token is not strict DER")
	// ErrTSATokenTooLarge: a token, reply or token file is over
	// maxTSAResponseBytes. Always joined with ErrTSATokenInvalid.
	ErrTSATokenTooLarge = errors.New("audit: timestamp token is too large")
	// ErrTSATokenFileRefused: a token path is not a regular file (a FIFO, a
	// device, a symlink or a directory), so it is not opened for reading.
	// Always joined with ErrTSATokenInvalid.
	ErrTSATokenFileRefused = errors.New("audit: timestamp token path is not a regular file")
)

// strictDERGate runs checkStrictDER and tags a failure with both sentinels.
// Every byte string handed to timestamp or pkcs7 passes through it first.
func strictDERGate(b []byte) error {
	if err := checkStrictDER(b); err != nil {
		return fmt.Errorf("%w: %w: %v", ErrTSATokenInvalid, ErrTSANotStrictDER, err)
	}
	return nil
}

// tokenLstat is os.Lstat; a test replaces it to simulate the path being
// swapped for a FIFO between the Lstat and the open.
var tokenLstat = os.Lstat

// readTokenFile reads a stored token without trusting the file: anything
// but a regular file is refused before it is opened (a FIFO would block the
// read), the open itself does not block, and at most maxTSAResponseBytes+1
// bytes are read, so a huge or sparse file is refused, not loaded.
func readTokenFile(path string) ([]byte, error) {
	path = filepath.Clean(path)
	fi, err := tokenLstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %w: %s is %v", ErrTSATokenInvalid, ErrTSATokenFileRefused, path, fi.Mode().Type())
	}
	f, err := os.OpenFile(path, os.O_RDONLY|tokenOpenNonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// Re-check on the open descriptor: the path may have been swapped.
	if fi, err := f.Stat(); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %w: %s is %v", ErrTSATokenInvalid, ErrTSATokenFileRefused, path, fi.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTSAResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTSAResponseBytes {
		return nil, fmt.Errorf("%w: %w: %s is larger than %d bytes", ErrTSATokenInvalid, ErrTSATokenTooLarge, path, maxTSAResponseBytes)
	}
	return data, nil
}

// canonicalAnchor is the exact record a TSA token covers. Field order is
// fixed by the struct, so the bytes are stable.
type canonicalAnchor struct {
	Checkpoint int64  `json:"checkpoint"`
	Root       string `json:"root"`
}

// CanonicalAnchorRecord is the byte string a TSA token's message imprint is
// the SHA-256 of: {"checkpoint":<id>,"root":"<root>"}, no whitespace.
func CanonicalAnchorRecord(checkpoint int64, root string) []byte {
	b, err := json.Marshal(canonicalAnchor{Checkpoint: checkpoint, Root: root})
	if err != nil {
		// Marshalling an int64 and a string cannot fail.
		panic(err)
	}
	return b
}

// AnchorRecordDigest is SHA-256(CanonicalAnchorRecord(checkpoint, root)).
func AnchorRecordDigest(checkpoint int64, root string) []byte {
	d := sha256.Sum256(CanonicalAnchorRecord(checkpoint, root))
	return d[:]
}

// TSATokenDir is where the tokens for the anchor file at anchorPath live.
func TSATokenDir(anchorPath string) string { return anchorPath + ".tsa" }

func tsaTokenName(checkpoint int64) string { return fmt.Sprintf("%020d.tsr", checkpoint) }

// TSATokenInfo is what a verified token says.
type TSATokenInfo struct {
	GenTime time.Time
	Imprint []byte
	Nonce   *big.Int
	Signer  string
}

// LoadTSATrustBundle reads a PEM file of the TSA root (and any intermediate)
// certificates tokens must chain to. A file with no certificate is an error.
func LoadTSATrustBundle(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("audit: read TSA trust bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("audit: TSA trust bundle %s holds no PEM certificate", path)
	}
	return pool, nil
}

// VerifyTSAToken checks a TimeStampToken: well-formed DER, a valid signature
// by the certificate it carries, that certificate chaining to trust at the
// token's genTime with the timeStamping extended key usage, and a SHA-256
// message imprint equal to digest.
func VerifyTSAToken(token []byte, trust *x509.CertPool, digest []byte) (TSATokenInfo, error) {
	info, err := verifyTSATokenSignature(token, trust)
	if err != nil {
		return info, err
	}
	if !bytes.Equal(info.Imprint, digest) {
		return info, ErrTSAImprintMismatch
	}
	return info, nil
}

// verifyTSATokenSignature is VerifyTSAToken without the imprint comparison.
func verifyTSATokenSignature(token []byte, trust *x509.CertPool) (TSATokenInfo, error) {
	if trust == nil {
		return TSATokenInfo{}, fmt.Errorf("%w: no trust bundle", ErrTSAUntrustedSigner)
	}
	if len(token) == 0 {
		return TSATokenInfo{}, fmt.Errorf("%w: token is empty", ErrTSATokenInvalid)
	}
	if len(token) > maxTSAResponseBytes {
		return TSATokenInfo{}, fmt.Errorf("%w: %w: %d bytes", ErrTSATokenInvalid, ErrTSATokenTooLarge, len(token))
	}
	// The pinned pkcs7 transcodes BER before parsing, and that transcoder
	// predates upstream's fixes for children that overrun their parent
	// (digitorus/pkcs7#23). Only strict DER reaches it. Moving to the patched
	// upstream needs Go 1.27: #3153.
	if err := strictDERGate(token); err != nil {
		return TSATokenInfo{}, err
	}
	// timestamp.Parse verifies the signature with the embedded certificate.
	ts, err := recoverParse(func() (*timestamp.Timestamp, error) { return timestamp.Parse(token) })
	if err != nil {
		return TSATokenInfo{}, fmt.Errorf("%w: %v", ErrTSATokenInvalid, err)
	}
	if !ts.AddTSACertificate || len(ts.Certificates) == 0 {
		return TSATokenInfo{}, fmt.Errorf("%w: the token carries no TSA certificate", ErrTSATokenInvalid)
	}
	if ts.HashAlgorithm != crypto.SHA256 {
		return TSATokenInfo{}, fmt.Errorf("%w: message imprint uses %v, not SHA-256", ErrTSATokenInvalid, ts.HashAlgorithm)
	}
	p7, err := recoverParse(func() (*pkcs7.PKCS7, error) { return pkcs7.Parse(token) })
	if err != nil {
		return TSATokenInfo{}, fmt.Errorf("%w: %v", ErrTSATokenInvalid, err)
	}
	if len(p7.Signers) != 1 {
		return TSATokenInfo{}, fmt.Errorf("%w: %d signers, want 1", ErrTSATokenInvalid, len(p7.Signers))
	}
	intermediates := x509.NewCertPool()
	for _, c := range p7.Certificates {
		intermediates.AddCert(c)
	}
	// Chain checked at genTime (Q6), so a token stays verifiable after the
	// TSA certificate expires.
	err = p7.VerifyWithOpts(x509.VerifyOptions{
		Roots:         trust,
		Intermediates: intermediates,
		CurrentTime:   ts.Time,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
	if err != nil {
		return TSATokenInfo{}, fmt.Errorf("%w: %v", ErrTSAUntrustedSigner, err)
	}
	signer := ""
	if c := p7.GetOnlySigner(); c != nil {
		signer = c.Subject.String()
	}
	return TSATokenInfo{GenTime: ts.Time, Imprint: ts.HashedMessage, Nonce: ts.Nonce, Signer: signer}, nil
}

// recoverParse turns a parser panic on hostile input into an error.
func recoverParse[T any](f func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parser panic: %v", r)
		}
	}()
	return f()
}

// checkStrictDER accepts exactly one DER element: definite, minimal lengths
// only, every child inside its parent, nesting at most maxTSADERDepth.
func checkStrictDER(b []byte) error {
	rest, err := walkDER(b, 0)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("trailing data after the DER element")
	}
	return nil
}

func walkDER(b []byte, depth int) ([]byte, error) {
	if depth > maxTSADERDepth {
		return nil, errors.New("DER nesting too deep")
	}
	var v asn1.RawValue
	// encoding/asn1 rejects indefinite and non-minimal lengths and lengths
	// past the end of b.
	rest, err := asn1.Unmarshal(b, &v)
	if err != nil {
		return nil, err
	}
	if v.IsCompound {
		inner := v.Bytes
		for len(inner) > 0 {
			if inner, err = walkDER(inner, depth+1); err != nil {
				return nil, err
			}
		}
	}
	return rest, nil
}

// tsaPostFunc sends one DER TimeStampReq and returns the raw reply. A field
// on the sink so tests substitute a fake TSA.
