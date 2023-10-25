package threatdetect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func newTestMinerSigRule(t *testing.T, store ConfigKV) *MinerSignatureRule {
	t.Helper()
	r, err := NewMinerSignatureRule(context.Background(), store)
	if err != nil {
		t.Fatalf("NewMinerSignatureRule: %v", err)
	}
	return r
}

// execEv builds an exec whose argv is argv split on spaces: one argument
// per word. Use execArgs for an argument that itself holds a space.
func execEv(filename, comm, argv string) ExecEvent {
	return execArgs(filename, comm, strings.Fields(argv)...)
}

// execArgs builds an exec with the real argument vector args, as the kernel
// hands it over (NUL-separated, so an argument may hold spaces).
func execArgs(filename, comm string, args ...string) ExecEvent {
	return ExecEvent{Container: "box-1", TenantID: "t1", At: time.Unix(100, 0), PID: 4242, PPID: 1, UID: 1000,
		Comm: comm, Filename: filename, Argv: args}
}

// TestNormalizeMinerSignature_RejectsCatchAll (#2638 review): a pattern that
// would match every exec is refused at insert — with kill_process on it
// would SIGKILL every process in every box. Name globs need a literal
// character; argv markers need minMinerSigArgvLen of them.
func TestNormalizeMinerSignature_RejectsCatchAll(t *testing.T) {
	name, argv := pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV
	tests := []struct {
		kind    pb.MinerSignatureKind
		pattern string
		want    string // normalized form, or "" when refused
		wantErr string // substring of the error when refused
	}{
		// name: anything-goes globs
		{name, "*", "", "matches every basename"},
		{name, "?*", "", "matches every basename"},
		{name, "**", "", "matches every basename"},
		{name, "?", "", "matches every basename"},
		{name, "???", "", "matches every basename"},
		{name, "[a-z]*", "", "matches every basename"},
		{name, "[^.]*", "", "matches every basename"},
		{name, " * ", "", "matches every basename"},
		{name, "   ", "", "is empty"},
		// name: one literal character is a choice, not a typo
		{name, "*m*", "*m*", ""},
		{name, "x*", "x*", ""},
		{name, "\\*", "\\*", ""},
		{name, "cpuminer*", "cpuminer*", ""},
		{name, "XMRig", "xmrig", ""},
		// argv literal: too short to single a miner out
		{argv, "a", "", "too short to be a signature"},
		{argv, "-o", "", "too short to be a signature"},
		{argv, "abc", "", "too short to be a signature"},
		{argv, " ab ", "", "too short to be a signature"},
		{argv, "   ", "", "is empty"},
		{argv, "abcd", "abcd", ""},
		{argv, "--coin ", "--coin ", ""},
		{argv, "stratum+tcp://", "stratum+tcp://", ""},
		// argv glob: tokens must carry literal characters, enough of them
		{argv, "*", "", "matches every command line"},
		{argv, "* *", "", "matches every command line"},
		{argv, "? *", "", "matches every command line"},
		{argv, "[a-z]*", "", "matches every command line"},
		{argv, "-o *", "", "too short to be a signature"},
		{argv, "-o *:3333", "-o *:3333", ""},
		{argv, "--coin=*", "--coin=*", ""},
		{argv, "-o[^/]*:3333", "-o[^/]*:3333", ""},
	}
	for _, tt := range tests {
		t.Run(MinerSignatureKindName(tt.kind)+":"+tt.pattern, func(t *testing.T) {
			got, err := NormalizeMinerSignature(tt.kind, tt.pattern)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("accepted %q as %q, want an error containing %q", tt.pattern, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				if !errors.Is(err, ErrMinerSignatureInvalid) {
					t.Fatalf("error = %v, want ErrMinerSignatureInvalid so the server maps it to InvalidArgument", err)
				}
				if tt.wantErr != "is empty" && !errors.Is(err, ErrMinerSignatureCatchAll) {
					t.Fatalf("error = %v, want ErrMinerSignatureCatchAll so the server maps it to InvalidArgument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused %q: %v", tt.pattern, err)
			}
			if got != tt.want {
				t.Fatalf("normalized = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNormalizeMinerSignature_InvalidSentinel (#2768 item 5): every refusal,
// whatever the kind, wraps ErrMinerSignatureInvalid, the one sentinel the
// server maps to InvalidArgument; AddSignature and RemoveSignature return it
// unchanged, so the server need not normalize first.
func TestNormalizeMinerSignature_InvalidSentinel(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	for _, c := range []struct {
		kind    pb.MinerSignatureKind
		pattern string
	}{
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, "/tmp/xmrig"},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, "xm[rig"},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV, "-o [a-"},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256, "abcd"},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256, strings.Repeat("z", 64)},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_UNSPECIFIED, "xmrig"},
	} {
		if _, err := NormalizeMinerSignature(c.kind, c.pattern); !errors.Is(err, ErrMinerSignatureInvalid) {
			t.Errorf("Normalize(%v, %q) = %v, want ErrMinerSignatureInvalid", c.kind, c.pattern, err)
		}
		if _, err := r.AddSignature(context.Background(), c.kind, c.pattern, ""); !errors.Is(err, ErrMinerSignatureInvalid) {
			t.Errorf("AddSignature(%v, %q) = %v, want ErrMinerSignatureInvalid", c.kind, c.pattern, err)
		}
		if err := r.RemoveSignature(context.Background(), c.kind, c.pattern); !errors.Is(err, ErrMinerSignatureInvalid) {
			t.Errorf("RemoveSignature(%v, %q) = %v, want ErrMinerSignatureInvalid", c.kind, c.pattern, err)
		}
	}
}

// TestMinerSignatureRule_AddRefusesCatchAll: the guard holds on the insert
// path too, and a refused add leaves the list untouched.
func TestMinerSignatureRule_AddRefusesCatchAll(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	before := len(r.ListSignatures())
	for _, c := range []struct {
		kind    pb.MinerSignatureKind
		pattern string
	}{
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, "*"},
		{pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV, "-o"},
	} {
		if _, err := r.AddSignature(context.Background(), c.kind, c.pattern, "typo"); !errors.Is(err, ErrMinerSignatureCatchAll) {
			t.Fatalf("AddSignature(%s, %q) = %v, want ErrMinerSignatureCatchAll", MinerSignatureKindName(c.kind), c.pattern, err)
		}
	}
	if n := len(r.ListSignatures()); n != before {
		t.Fatalf("list grew from %d to %d on refused adds", before, n)
	}
	if got := r.OnExec(RuleContext{}, execEv("/usr/sbin/sshd", "sshd", "sshd -D")); len(got) != 0 {
		t.Fatalf("sshd matched after refused adds: %+v", got)
	}
}

// TestMinerSignatureBaselineParses fails the build on a malformed embedded
// list, and pins the #2711 Q2 decision: the shipped baseline carries name
// and argv entries only — no sha256.
func TestMinerSignatureBaselineParses(t *testing.T) {
	version, entries, err := parseBaselineMinerSigYAML(embeddedMinerSigYAML)
	if err != nil {
		t.Fatalf("embedded baseline: %v", err)
	}
	if version == "" || len(entries) == 0 {
		t.Fatalf("version=%q entries=%d, want a versioned, non-empty list", version, len(entries))
	}
	kinds := map[pb.MinerSignatureKind]int{}
	for _, e := range entries {
		kinds[e.Kind]++
		if !e.Builtin {
			t.Errorf("%s: baseline entry not marked builtin", e.Subject())
		}
	}
	if kinds[pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256] != 0 {
		t.Errorf("baseline carries %d sha256 entries; the shipped list is name and argv only (#2711 Q2)", kinds[pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256])
	}
	for _, name := range []string{"xmrig", "minerd", "cpuminer*", "nanominer", "t-rex", "lolminer", "nbminer", "gminer"} {
		if !hasEntry(entries, pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, name) {
			t.Errorf("baseline missing name %q", name)
		}
	}
	for _, marker := range []string{"--donate-level", "stratum+tcp://", "stratum+ssl://", "--coin ", "--coin=*", "-o *:3333", "-o *:14444", "--url=*:3333", "--url *:14433", "-o[^/]*:7777"} {
		if !hasEntry(entries, pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV, marker) {
