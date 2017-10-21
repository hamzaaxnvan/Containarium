package releases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchSHA256_FindsNamedBinary pins the GNU sha256sum-style parsing
// (SHA256SUMS.txt lines are "<hash>  <filename>") against a fixture that
// carries several binaries, only one of which is asked for.
func TestFetchSHA256_FindsNamedBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt") {
