// Package postgres isolates PostgreSQL-backed test binaries. It is test support
// only; production connection configuration must not use this package.
package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const dsnEnv = "CONTAINARIUM_TEST_DSN"
const forceEnv = "CONTAINARIUM_TEST_DSN_FORCE"

var testDatabase = regexp.MustCompile(`(^|[_-])test([_-]|$)`)

// Run is called by TestMain before any test can connect. Every package binary
// gets its own schema, including concurrent invocations of the same package.
// Updating the process-local DSN also isolates reconnects and restart tests.
// An unset DSN retains the package's existing skip/fail behavior.
func Run(run func() int) int {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		return run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	scoped, cleanup, err := newSchema(ctx, dsn, os.Getenv(forceEnv))
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "PostgreSQL test setup:", err)
		return 1
	}
	if err := os.Setenv(dsnEnv, scoped); err != nil {
		fmt.Fprintln(os.Stderr, "set isolated test DSN:", err)
		if err := cleanup(); err != nil {
			fmt.Fprintln(os.Stderr, "PostgreSQL test cleanup:", err)
		}
		return 1
	}
	code := run()
	if err := os.Setenv(dsnEnv, dsn); err != nil {
		fmt.Fprintln(os.Stderr, "restore test DSN:", err)
		code = 1
	}
	if err := cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "PostgreSQL test cleanup:", err)
		code = 1
	}
	return code
}
