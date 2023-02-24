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
