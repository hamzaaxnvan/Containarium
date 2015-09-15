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
			t.Errorf("baseline missing argv marker %q", marker)
		}
	}
}

func hasEntry(entries []MinerSignatureEntry, kind pb.MinerSignatureKind, pattern string) bool {
	for _, e := range entries {
		if e.Kind == kind && e.Pattern == pattern {
			return true
		}
	}
	return false
}

func TestMinerSignatureBaseline_MalformedRejected(t *testing.T) {
	for name, raw := range map[string]string{
		"not yaml":       "version: [",
		"no version":     "entries:\n  - {kind: name, pattern: xmrig}\n",
		"unknown kind":   "version: 1\nentries:\n  - {kind: md5, pattern: abc}\n",
		"bad glob":       "version: 1\nentries:\n  - {kind: name, pattern: \"xm[rig\"}\n",
		"short sha256":   "version: 1\nentries:\n  - {kind: sha256, pattern: abcd}\n",
		"name with path": "version: 1\nentries:\n  - {kind: name, pattern: /tmp/xmrig}\n",
		"empty argv":     "version: 1\nentries:\n  - {kind: argv, pattern: \"  \"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newMinerSignatureRule(context.Background(), nil, []byte(raw)); err == nil {
				t.Fatal("malformed baseline accepted")
			}
		})
	}
}

// TestMinerSignatureRule_Matchers is the matcher table over the baseline
// (name glob, argv literal, argv token glob incl. -o host:<stratum port>).
func TestMinerSignatureRule_Matchers(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	tests := []struct {
		name        string
		ev          ExecEvent
		wantSubject string // "" = no finding
	}{
		{"bare name", execEv("/usr/local/bin/xmrig", "xmrig", "xmrig"), "name:xmrig"},
		{"hidden dir", execEv("/tmp/.x/xmrig", "xmrig", "./xmrig"), "name:xmrig"},
		{"case-insensitive", execEv("/opt/XMRig", "XMRig", "./XMRig"), "name:xmrig"},
		{"glob cpuminer-multi", execEv("/opt/cpuminer-multi", "cpuminer-multi", "./cpuminer-multi"), "name:cpuminer*"},
		{"comm only (renamed path)", execEv("/usr/bin/.cache", "t-rex", "/usr/bin/.cache"), "name:t-rex"},
		{"argv despite benign name", execEv("/usr/bin/gcc", "gcc", "gcc --donate-level 1"), "argv:--donate-level"},
		{"stratum url in node", execEv("/usr/bin/node", "node", "node pool.js stratum+tcp://pool.example:443"), "argv:stratum+tcp://"},
		{"stratum tls url", execEv("/usr/bin/python3", "python3", "python3 m.py --url=stratum+ssl://pool.example:443"), "argv:stratum+ssl://"},
		{"argv uppercase", execEv("/usr/bin/svc", "svc", "svc STRATUM+TCP://pool.example:443"), "argv:stratum+tcp://"},
		{"coin flag", execEv("/usr/bin/svc", "svc", "svc --coin monero"), "argv:--coin "},
		{"-o host:3333", execEv("/usr/bin/svc", "svc", "./svc -o pool.example:3333 -u wallet"), "argv:-o *:3333"},
		{"-o host:14444", execEv("/usr/bin/svc", "svc", "./svc -a rx -o 198.51.100.7:14444"), "argv:-o *:14444"},
		{"xmrig -o pool:3333", execEv("/opt/x", "x", "xmrig -o pool.example.com:3333"), "argv:-o *:3333"},
		{"-o stratum url still caught by the literal", execEv("/opt/x", "x", "./x -o stratum+tcp://pool:3333"), "argv:stratum+tcp://"},
		{"--coin=monero", execEv("/usr/bin/svc", "svc", "svc --coin=monero"), "argv:--coin=*"},
		{"ssh -o ProxyJump=host:4444", execEv("/usr/bin/ssh", "ssh", "ssh -o ProxyJump=bastion:4444 host"), ""},
		{"ssh -o Port=3333", execEv("/usr/bin/ssh", "ssh", "ssh -o Port=3333 host"), ""},
		{"ssh -o key=value with stratum port", execEv("/usr/bin/ssh", "ssh", "ssh -o ProxyCommand=nc:3333 host"), ""},
		{"--url=host:3333", execEv("/opt/x", "x", "./x --url=pool.example:3333"), "argv:--url=*:3333"},
		{"--url host:4444", execEv("/opt/x", "x", "./x --url pool.example:4444"), "argv:--url *:4444"},
		{"-ohost:5555 attached", execEv("/opt/x", "x", "./x -opool.example:5555"), "argv:-o[^/]*:5555"},
		{"host part is case-insensitive", execEv("/opt/x", "x", "./x -o POOL.Example:3333"), "argv:-o *:3333"},
		{"-O is not -o", execEv("/usr/bin/cc", "cc", "cc -O pool:3333"), ""},
		{"-O attached is not -o", execEv("/usr/bin/cc", "cc", "cc -Opool:3333"), ""},
		{"--url with a scheme is not host:port", execEv("/usr/bin/curl", "curl", "curl --url=http://h:3333/x"), ""},
		{"-o attached path", execEv("/usr/bin/ld", "ld", "ld -o/out/a:3333"), ""},
		{"gcc -O2", execEv("/usr/bin/gcc", "gcc", "gcc -O2 -o main main.c"), ""},
		{"-o on a non-stratum port", execEv("/usr/bin/svc", "svc", "./svc -o pool.example:443"), ""},
		{"-o output file named like a port", execEv("/usr/bin/ld", "ld", "ld -o out/a:3333.o"), ""},
		{"--coins is not --coin ", execEv("/usr/bin/app", "app", "app --coins=3"), ""},
		{"empty event", ExecEvent{Container: "box-1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.OnExec(RuleContext{}, tt.ev)
			if tt.wantSubject == "" {
				if len(got) != 0 {
					t.Fatalf("finding %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Subject != "container:box-1:"+tt.wantSubject {
				t.Fatalf("findings %+v, want one with subject %q", got, tt.wantSubject)
			}
		})
	}
}

// TestMinerSignatureRule_ArgvArgumentBoundaries (#2768 item 4, option A): a
// literal argv marker matches only where a real argument begins, using the
// NUL-separated argv rather than the space-joined line, so a marker quoted
// inside an argument (a commit message, an echo) is a mention, not a hit.
// A space inside a marker is an argument boundary ("--coin " is the
// argument "--coin" followed by another). The value of a "--opt=value"
// argument counts as an argument start: getopt reads it as "--opt value".
// Glob markers match whole real arguments the same way.
func TestMinerSignatureRule_ArgvArgumentBoundaries(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	tests := []struct {
		name        string
		ev          ExecEvent
		wantSubject string // "" = no finding
	}{
		// the mention class: a marker inside one quoted argument
		{"git message mentioning a stratum url", execArgs("/usr/bin/git", "git", "git", "commit", "-m", "document stratum+tcp:// pool format"), ""},
		{"git message starting a word with the marker", execArgs("/usr/bin/git", "git", "git", "commit", "-m", "see stratum+ssl://pool:443 docs"), ""},
		{"echo of --donate-level inside one argument", execArgs("/bin/echo", "echo", "echo", "set --donate-level 1 before release"), ""},
		{"--coin inside one argument", execArgs("/usr/bin/git", "git", "git", "commit", "-m", "--coin monero"), ""},
		{"-o host:3333 inside one argument", execArgs("/bin/echo", "echo", "echo", "use -o pool.example:3333"), ""},
		{"marker mid-argument", execArgs("/usr/bin/app", "app", "app", "--input=notes-stratum+tcp://x"), ""},
		{"marker after a non-option '='", execArgs("/usr/bin/env", "env", "env", "POOL=stratum+tcp://pool:443", "app"), ""},
		{"--coin as the last argument", execArgs("/usr/bin/svc", "svc", "svc", "--coin"), ""},
		// real arguments still hit
		{"stratum url argument", execArgs("/usr/bin/node", "node", "node", "pool.js", "stratum+tcp://pool.example:443"), "argv:stratum+tcp://"},
		{"--opt=value counts as an argument start", execArgs("/opt/x", "x", "./x", "--url=stratum+ssl://pool.example:443"), "argv:stratum+ssl://"},
		{"--donate-level flag", execArgs("/opt/x", "x", "./x", "--donate-level", "1"), "argv:--donate-level"},
		{"--donate-level=1", execArgs("/opt/x", "x", "./x", "--donate-level=1"), "argv:--donate-level"},
		{"--coin then its value", execArgs("/usr/bin/svc", "svc", "svc", "--coin", "monero"), "argv:--coin "},
		{"uppercase marker argument", execArgs("/usr/bin/svc", "svc", "svc", "STRATUM+TCP://pool.example:443"), "argv:stratum+tcp://"},
		{"-o host:3333 as two arguments", execArgs("/usr/bin/svc", "svc", "./svc", "-o", "pool.example:3333"), "argv:-o *:3333"},
		// the acknowledged limitation (option A): an argument that begins
		// with the marker is a hit whatever the command
		{"grep for the flag still hits (documented limitation)", execArgs("/usr/bin/grep", "grep", "grep", "-r", "--donate-level", "docs/"), "argv:--donate-level"},
		{"an argument beginning with -o matches the attached form", execArgs("/bin/echo", "echo", "echo", "-o pool.example:3333"), "argv:-o[^/]*:3333"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.OnExec(RuleContext{}, tt.ev)
			if tt.wantSubject == "" {
				if len(got) != 0 {
					t.Fatalf("finding %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Subject != "container:box-1:"+tt.wantSubject {
				t.Fatalf("findings %+v, want one with subject %q", got, tt.wantSubject)
			}
		})
	}
}

// TestMinerSignatureRule_MultiPartLiteral (#2768): in an operator marker with
// spaces, every part before the last is a whole argument and the last begins
// the next one.
func TestMinerSignatureRule_MultiPartLiteral(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	argv := pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV
	for _, p := range []string{"--algo rx/0", "-a rx -o"} {
		if _, err := r.AddSignature(context.Background(), argv, p, "test"); err != nil {
			t.Fatalf("AddSignature(%q): %v", p, err)
		}
	}
	tests := []struct {
		name        string
		args        []string
		wantSubject string
	}{
		{"two parts", []string{"./x", "--algo", "rx/0"}, "argv:--algo rx/0"},
		{"last part is a prefix", []string{"./x", "--algo", "rx/0-fast"}, "argv:--algo rx/0"},
		{"first part must be the whole argument", []string{"./x", "--algorithm", "rx/0"}, ""},
		{"three parts", []string{"./x", "-a", "rx", "-o", "pool:443"}, "argv:-a rx -o"},
		{"middle part must be the whole argument", []string{"./x", "-a", "rxx", "-o", "pool:443"}, ""},
		{"parts in one argument", []string{"./x", "--algo rx/0"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.OnExec(RuleContext{}, execArgs("/opt/x", "x", tt.args...))
			if tt.wantSubject == "" {
				if len(got) != 0 {
					t.Fatalf("finding %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Subject != "container:box-1:"+tt.wantSubject {
				t.Fatalf("findings %+v, want one with subject %q", got, tt.wantSubject)
			}
		})
	}
}

// TestMinerSignatureRule_FindingCarriesExec: a hit is one HIGH
// MINER_SIGNATURE finding for the event's container and tenant with the
// exec attached as evidence and `matched` set.
func TestMinerSignatureRule_FindingCarriesExec(t *testing.T) {
	r := newTestMinerSigRule(t, nil)
	ev := execEv("/tmp/xmrig", "xmrig", "./xmrig --donate-level 1")
	ev.ArgvTruncated = true
	ev.StartTime = 987654 // #2765: the kill compares it against the pid's
	got := r.OnExec(RuleContext{}, ev)
