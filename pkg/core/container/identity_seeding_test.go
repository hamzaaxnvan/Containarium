package container

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	"github.com/footprintai/containarium/pkg/core/ostype"
)

var errWedgedForTest = errors.New("command execution timed out (simulated wedge)")

// The collapse contract (#1488 Finding 2): identity seeding pays one
// in-guest exec per function, not one per command. Each Incus Exec is a
// websocket-upgrading operation at ~50-150ms, so the exec COUNT is the
// behavior under test, alongside the safety of the generated script.

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice", "'alice'"},
		{"", "''"},
		{"a b", "'a b'"},
		{"a'b", `'a'\''b'`},
		{"'; touch /pwned; '", `''\''; touch /pwned; '\'''`},
		{"$HOME `id` \\", "'$HOME `id` \\'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"adduser", "--gecos", "", "a'b"})
	want := `'adduser' '--gecos' '' 'a'\''b'`
	if got != want {
		t.Errorf("shellJoin = %s, want %s", got, want)
	}
}

// seedRecorder captures every Exec and WriteFile.
type seedRecorder struct {
	execs  [][]string
