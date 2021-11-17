package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// grafanaIniWithPassword is a provisioned grafana.ini whose [database]
// password is pw. A second section carries its own `password` key, to prove
// the rewrite touches only [database].
func grafanaIniWithPassword(pw string) string {
	return "[database]\ntype = postgres\nhost = 10.100.0.242:5432\nname = grafana\nuser = containarium\npassword = " + pw + "\nssl_mode = disable\n\n" +
		"[smtp]\npassword = keep-me\n\n[auth.anonymous]\nenabled = false\n"
}

func TestGrafanaDBSettings(t *testing.T) {
	host, user, pw, ok := grafanaDBSettings(grafanaIniWithPassword("old-pass"))
	if !ok {
		t.Fatal("ok = false for a provisioned ini")
	}
	if host != "10.100.0.242:5432" || user != "containarium" || pw != "old-pass" {
		t.Fatalf("got host=%q user=%q pw=%q", host, user, pw)
	}

	if _, _, _, ok := grafanaDBSettings("[server]\nhttp_port = 3000\n"); ok {
		t.Error("an ini with no [database] section must report ok=false")
	}
	// A password key in another section is not the database password.
	if _, _, _, ok := grafanaDBSettings("[smtp]\npassword = x\n[database]\nhost = h:5432\nuser = u\n"); ok {
		t.Error("an ini with no [database] password must report ok=false")
	}
}

func TestGrafanaDBSettings_TripleQuotedValue(t *testing.T) {
	ini := "[database]\nhost = h:5432\nuser = u\npassword = \"\"\"pa#ss;word\"\"\"\n"
	_, _, pw, ok := grafanaDBSettings(ini)
	if !ok || pw != "pa#ss;word" {
		t.Fatalf("pw = %q ok=%v, want the unquoted value", pw, ok)
	}
}

func TestWithGrafanaDBPassword(t *testing.T) {
	t.Run("rewrites only the [database] password", func(t *testing.T) {
		in := grafanaIniWithPassword("old-pass")
		out, changed := withGrafanaDBPassword(in, "new-pass")
		if !changed {
			t.Fatal("changed = false")
		}
		if _, _, pw, _ := grafanaDBSettings(out); pw != "new-pass" {
			t.Fatalf("database password = %q after rewrite", pw)
		}
		if !strings.Contains(out, "[smtp]\npassword = keep-me") {
			t.Errorf("another section's password was touched:\n%s", out)
		}
		for _, keep := range []string{"host = 10.100.0.242:5432", "user = containarium", "ssl_mode = disable", "[auth.anonymous]\nenabled = false"} {
			if !strings.Contains(out, keep) {
				t.Errorf("line %q lost on rewrite:\n%s", keep, out)
			}
		}
	})
	t.Run("already equal is a no-op", func(t *testing.T) {
		in := grafanaIniWithPassword("same")
		out, changed := withGrafanaDBPassword(in, "same")
		if changed || out != in {
			t.Fatalf("converged ini was rewritten (changed=%v)", changed)
		}
	})
	t.Run("no [database] section is left alone", func(t *testing.T) {
		in := "[server]\nhttp_port = 3000\n"
		out, changed := withGrafanaDBPassword(in, "x")
		if changed || out != in {
			t.Fatalf("an ini with nothing to rewrite was modified (changed=%v)", changed)
		}
	})
	t.Run("a password Grafana would read as a comment is quoted", func(t *testing.T) {
		out, changed := withGrafanaDBPassword(grafanaIniWithPassword("old"), "ab#cd;ef")
		if !changed {
			t.Fatal("changed = false")
		}
		if _, _, pw, _ := grafanaDBSettings(out); pw != "ab#cd;ef" {
			t.Fatalf("round-trip = %q, want ab#cd;ef\n%s", pw, out)
		}
		if !strings.Contains(out, `"""ab#cd;ef"""`) {
			t.Errorf("value containing # or ; must be triple-quoted:\n%s", out)
		}
	})
}

