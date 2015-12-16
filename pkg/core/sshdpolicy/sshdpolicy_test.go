package sshdpolicy

import (
	"strings"
	"testing"
)

func TestDirective(t *testing.T) {
	tests := []struct {
		name, config, key, def, want string
	}{
		{"absent uses default", "Port 22\n", "PasswordAuthentication", "yes", "yes"},
		{"simple", "PasswordAuthentication no\n", "PasswordAuthentication", "yes", "no"},
		{"case insensitive key", "passwordauthentication NO\n", "PasswordAuthentication", "yes", "NO"},
		{"comments ignored", "#PasswordAuthentication yes\nPasswordAuthentication no\n", "PasswordAuthentication", "yes", "no"},
		// OpenSSH takes the FIRST occurrence, not the last.
		{"first occurrence wins", "PasswordAuthentication no\nPasswordAuthentication yes\n", "PasswordAuthentication", "yes", "no"},
		{"match block ignored", "Match User bob\nPasswordAuthentication yes\n", "PasswordAuthentication", "no", "no"},
		{"directive before match still counts", "PasswordAuthentication no\nMatch User bob\nPasswordAuthentication yes\n", "PasswordAuthentication", "yes", "no"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Directive(tc.config, tc.key, tc.def); got != tc.want {
				t.Errorf("Directive() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMerge_DropInsPrecedeMainInLexicalOrder(t *testing.T) {
	merged := Merge("PasswordAuthentication yes\n", []DropIn{
		{Name: "90-later.conf", Content: "PasswordAuthentication yes\n"},
		{Name: "README", Content: "PasswordAuthentication no\n"}, // not .conf: ignored, as sshd does
		{Name: "10-first.conf", Content: "PasswordAuthentication no\n"},
	})
	if got := Directive(merged, "PasswordAuthentication", "yes"); got != "no" {
		t.Fatalf("lexically-first drop-in must win under first-match-wins; merged:\n%s", merged)
	}
	if strings.Index(merged, "10-first") > strings.Index(merged, "90-later") && strings.Contains(merged, "10-first") {
		t.Error("drop-ins not in lexical order")
	}
}

// The managed drop-in must itself evaluate as compliant, and must win over
// every drop-in a distro or cloud-init ships (they are 50-*.conf).
func TestDropInContent_IsCompliantAndSortsFirst(t *testing.T) {
	cloudInit := DropIn{Name: "50-cloud-init.conf", Content: "PasswordAuthentication yes\n"}
	ours := DropIn{Name: DropInName, Content: string(DropInContent())}
	r := Evaluate(Merge("PasswordAuthentication yes\n", []DropIn{cloudInit, ours}))
	if !r.Compliant() {
