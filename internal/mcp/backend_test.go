package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newBackend classifies the target from the credential: a `ctnr_` hosted-
// control-plane API token → the cloud backend; a JWT (or nothing) → the OSS
// daemon backend.
func TestBackend_TargetClassification(t *testing.T) {
	if _, ok := newBackend(&Config{ServerURL: "https://cloud.example", JWTToken: "ctnr_abc.def"}).(cloudClient); !ok {
		t.Fatal("ctnr_ token must classify as the cloud backend")
	}
	if _, ok := newBackend(&Config{ServerURL: "https://daemon.example", JWTToken: "eyJhbGciOi.jwt"}).(*Client); !ok {
