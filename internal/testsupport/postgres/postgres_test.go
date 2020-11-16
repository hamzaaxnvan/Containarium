package postgres

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestValidateDSN(t *testing.T) {
	for _, tc := range []struct {
		name, dsn, force string
		ok               bool
	}{
		{"test database URL", "postgres://user@localhost/containarium_test", "", true},
		{"test prefix", "postgresql://user@localhost/test_2749", "", true},
		{"test token", "dbname=containarium-test-2749", "", true},
		{"keyword database", "dbname=containarium_test", "", true},
		{"URL application", "postgres://user@localhost/production?application_name=containarium-test", "", true},
		{"keyword application", "dbname=production application_name=containarium-test", "", true},
		{"unsafe", "postgres://user@localhost/production", "", false},
		{"substring is not identity", "dbname=contest", "", false},
		{"password is not identity", "postgres://user:containarium-test@localhost/production", "", false},
		{"application must match", "dbname=production application_name=containarium-test-other", "", false},
		{"force", "dbname=production", "1", true},
		{"force must be explicit", "dbname=production", "true", false},
		{"force still parses", "postgres://user@localhost:bad/production", "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDSN(tc.dsn, tc.force)
			if (err == nil) != tc.ok {
				t.Fatalf("validateDSN = %v, want accepted=%v", err, tc.ok)
			}
		})
	}
}

func TestSchemaDSN(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:secret@localhost/containarium_test?search_path=public&application_name=containarium-test",
		"postgresql://user@localhost/containarium_test?search_path=public&search_path=other",
		"dbname=containarium_test user=user password='a b' search_path=public",
	} {
		cfg, err := pgx.ParseConfig(schemaDSN(dsn, "test_012345"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RuntimeParams["search_path"] != "test_012345" {
			t.Fatalf("search_path = %q", cfg.RuntimeParams["search_path"])
		}
		if cfg.Database != "containarium_test" || cfg.User != "user" {
			t.Fatal("connection identity changed")
		}
	}
}

func TestRunRejectsUnsafeBeforeConnecting(t *testing.T) {
	t.Setenv(dsnEnv, "postgres://user@127.0.0.1:1/production")
	t.Setenv(forceEnv, "")
	called := false
	if code := Run(func() int { called = true; return 0 }); code != 1 || called {
		t.Fatalf("code=%d, tests ran=%v", code, called)
	}
}

func TestRunWithoutDSN(t *testing.T) {
	t.Setenv(dsnEnv, "")
	if code := Run(func() int { return 7 }); code != 7 {
		t.Fatalf("code=%d", code)
