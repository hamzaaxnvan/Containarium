package secrets

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// callSitePattern matches an actual call — `<something>.BrokerCredential(`
// — not the method's own declaration, which reads `) BrokerCredential(`
// with no dot immediately before the name.
var callSitePattern = regexp.MustCompile(`\.BrokerCredential\(`)

// repoRoot walks up from this test file until it finds go.mod, so the
// scan works regardless of `go test`'s working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
