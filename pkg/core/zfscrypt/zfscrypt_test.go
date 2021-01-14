package zfscrypt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/zfskey"
)

// fakeRunner records every zfs invocation and replays canned responses.
//
// It proves the orchestration, not ZFS's own semantics — see the package
// doc and #1200. Where a test depends on how real ZFS behaves, that
// assumption is named in the test so it can be checked against a pool.
type fakeRunner struct {
	calls  [][]string
	stdins [][]byte

	// responses, keyed by the zfs subcommand (calls[0]).
	stdout map[string]string
	stderr map[string]string
	errs   map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		stdout: map[string]string{},
		stderr: map[string]string{},
		errs:   map[string]error{},
	}
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	// Copy: the caller may reuse or zero its buffer.
	cp := make([]byte, len(stdin))
	copy(cp, stdin)
	f.stdins = append(f.stdins, cp)

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	return f.stdout[sub], f.stderr[sub], f.errs[sub]
}

func (f *fakeRunner) lastCall() []string {
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

// ran reports whether any invocation's joined args contain the substring.
func (f *fakeRunner) ran(substr string) bool {
	return strings.Contains(f.allArgs(), substr)
}

func (f *fakeRunner) allArgs() string {
	var b strings.Builder
	for _, c := range f.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func testKey(t *testing.T, fill byte) zfskey.Key {
	t.Helper()
	k, err := zfskey.NewKey(bytes.Repeat([]byte{fill}, zfskey.KeyLen))
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

// AC (#1199): key material is piped to zfs via stdin — never on argv,
// never in a temp file.
//
// argv is world-readable through /proc/<pid>/cmdline for the life of the
// process, so a key there is a key disclosed to every local user. This
// is the single most important property in the package.
func TestKeyMaterialNeverReachesArgv(t *testing.T) {
	key := testKey(t, 0xAB)
	raw := string(key.Bytes())

	for _, tc := range []struct {
		name string
		call func(*Manager) error
	}{
		{"create", func(m *Manager) error {
			return m.CreateEncrypted(context.Background(), "pool/containers/alice", key)
		}},
		{"load", func(m *Manager) error {
			return m.LoadKey(context.Background(), "pool/containers/alice", key)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRunner()
			f.stdout["get"] = "unavailable" // so LoadKey proceeds
			m := NewManager(f)

			if err := tc.call(m); err != nil {
				t.Fatalf("call: %v", err)
			}

			args := f.allArgs()
			if strings.Contains(args, raw) {
				t.Error("key material appeared on argv — readable via /proc/<pid>/cmdline")
			}
			// Also catch it hidden inside a keylocation value.
			for _, c := range f.calls {
				for _, a := range c {
					if strings.Contains(a, raw) {
						t.Errorf("key material embedded in argument %q", a)
					}
				}
			}

			// It must have gone somewhere: stdin.
			var sawKeyOnStdin bool
			for _, in := range f.stdins {
				if bytes.Equal(in, key.Bytes()) {
					sawKeyOnStdin = true
				}
			}
			if !sawKeyOnStdin {
				t.Error("key was never written to stdin — it has to reach zfs somehow")
			}
