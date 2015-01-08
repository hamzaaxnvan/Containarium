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
