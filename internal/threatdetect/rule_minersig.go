package threatdetect

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// embeddedMinerSigYAML is the baseline miner-signature list (#2638): binary
// names and command-line markers of the common miners. Versioned like the
// bad-destination list; extended without a rebuild via AddSignature /
// `containarium security miner-signatures add`. It carries no sha256
// entries (decided on #2711 Q2); the hash matcher serves operator entries.
//
//go:embed rule_minersig_list.yaml
var embeddedMinerSigYAML []byte

// minerSigConfigKey is the DaemonConfigStore key operator-added entries are
// persisted under, JSON-encoded.
const minerSigConfigKey = "threatdetect.miner_signatures.operator"

// ErrMinerSignatureNotFound reports a remove of an entry that is not among
// the operator-added ones.
var ErrMinerSignatureNotFound = errors.New("miner signature not found among operator-added entries")

// ErrMinerSignatureBuiltin reports a remove of a baseline entry.
var ErrMinerSignatureBuiltin = errors.New("miner signature is a baseline entry and cannot be removed")

// ErrMinerSignatureCatchAll reports a pattern that would match (nearly)
// every exec: a name glob with no literal character ("*", "?*", "[a-z]*")
// or an argv marker too short to single anything out. With kill_process on,
// such an entry — a typo for "*miner*", say — would SIGKILL every process in
// every box, so it is refused at insert (#2638 review).
var ErrMinerSignatureCatchAll = errors.New("miner signature matches every process")

// ErrMinerSignatureInvalid wraps every refusal of NormalizeMinerSignature
// (empty, malformed, unknown kind, catch-all): the caller gave a pattern
// that is not a signature. The server maps it to InvalidArgument.
var ErrMinerSignatureInvalid = errors.New("invalid miner signature")

// minMinerSigArgvLen is the fewest literal characters an argv marker must
// carry — "-o *" or "ab" tell a miner from nothing. Baseline entries are
// held to it too (TestMinerSignatureBaselineParses).
const minMinerSigArgvLen = 4

// MinerSignatureEntry is one entry of the miner-signature list, mirroring
// pb.MinerSignature. Pattern is stored normalized (see
// NormalizeMinerSignature).
type MinerSignatureEntry struct {
	Kind    pb.MinerSignatureKind
	Pattern string
	Label   string
	// Builtin marks a baseline entry shipped with the daemon.
	Builtin bool
}

// ToProto renders the entry as the wire message.
func (e MinerSignatureEntry) ToProto() *pb.MinerSignature {
	return &pb.MinerSignature{Kind: e.Kind, Pattern: e.Pattern, Label: e.Label, Builtin: e.Builtin}
}

// Subject identifies the signature in evidence: "<kind>:<pattern>". Findings
// use a container-scoped namespace so their response belongs to one box.
func (e MinerSignatureEntry) Subject() string {
	return MinerSignatureKindName(e.Kind) + ":" + e.Pattern
}

// minerSigJSON is the persisted shape of one operator entry. Kind is the
// short name ("name", "argv", "sha256"), not the enum number, so the stored
// value stays readable and survives enum renumbering.
type minerSigJSON struct {
	Kind    string `json:"kind" yaml:"kind"`
	Pattern string `json:"pattern" yaml:"pattern"`
	Label   string `json:"label" yaml:"label"`
}

type minerSigYAMLFile struct {
	Version int            `yaml:"version"`
	Entries []minerSigJSON `yaml:"entries"`
}

var minerSigKindNames = map[pb.MinerSignatureKind]string{
	pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME:   "name",
	pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV:   "argv",
	pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256: "sha256",
}

// MinerSignatureKindName is the short lowercase name of k ("name", "argv",
// "sha256"); "" for UNSPECIFIED or an unknown value.
func MinerSignatureKindName(k pb.MinerSignatureKind) string { return minerSigKindNames[k] }

// ParseMinerSignatureKind reads a short name or the full enum name.
func ParseMinerSignatureKind(s string) (pb.MinerSignatureKind, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "miner_signature_kind_")
	for k, n := range minerSigKindNames {
		if n == s {
			return k, nil
		}
	}
	return pb.MinerSignatureKind_MINER_SIGNATURE_KIND_UNSPECIFIED, fmt.Errorf("unknown miner-signature kind %q: want name, argv or sha256", s)
}

// hasGlob reports whether s holds a path.Match metacharacter.
func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

// globLiteralLen counts the characters of a path.Match pattern that must
// match literally: everything outside '*', '?' and a "[...]" class (an
// escaped character counts as one literal). Zero means the pattern
// constrains nothing but length. p has already passed path.Match.
func globLiteralLen(p string) int {
	n := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?':
		case '[':
			// Skip the class up to its closing ']' (path.Match refuses ']'
			// as a first member, so the first ']' is the closing one).
			if k := strings.IndexByte(p[i+1:], ']'); k >= 0 {
				i += k + 1
			} else {
				i = len(p)
			}
		case '\\':
			if i+1 < len(p) {
				i++
			}
			n++
		default:
			n++
		}
	}
	return n
}

// NormalizeMinerSignature validates pattern for kind and returns it in the
// form it is stored and matched in: lowercased (matching is
// case-insensitive) except for an argv glob, whose option tokens match
// case-sensitively; surrounding whitespace trimmed except for an argv
// literal, whose trailing space is significant ("--coin "). Every refusal
// wraps ErrMinerSignatureInvalid (and a catch-all also ErrMinerSignatureCatchAll).
func NormalizeMinerSignature(kind pb.MinerSignatureKind, pattern string) (string, error) {
	p, err := normalizeMinerSignature(kind, pattern)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMinerSignatureInvalid, err)
	}
	return p, nil
}

func normalizeMinerSignature(kind pb.MinerSignatureKind, pattern string) (string, error) {
	switch kind {
	case pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME:
