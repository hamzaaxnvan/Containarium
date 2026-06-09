package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// makeUnsignedJWT crafts a JWT with the given claims and a
// dummy signature segment. scopesFromJWT doesn't verify the
// signature (the daemon does that on the actual API call),
// so this is enough for the MCP-side filter tests.
func makeUnsignedJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(claims)
	enc := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return strings.Join([]string{enc(hb), enc(pb), "sig"}, ".")
}

func TestScopesFromJWT_PresentClaim(t *testing.T) {
	tok := makeUnsignedJWT(t, map[string]interface{}{
		"username": "alice",
		"scopes":   []string{"containers:read", "secrets:read"},
	})
	got, ok := scopesFromJWT(tok)
	if !ok {
		t.Fatal("expected parsed=true")
	}
	if len(got) != 2 || got[0] != "containers:read" || got[1] != "secrets:read" {
		t.Fatalf("scopes = %v", got)
	}
}

func TestScopesFromJWT_AbsentClaim(t *testing.T) {
	tok := makeUnsignedJWT(t, map[string]interface{}{
		"username": "alice",
	})
	got, ok := scopesFromJWT(tok)
	if !ok {
		t.Fatal("expected parsed=true even without scopes claim")
	}
	if got != nil {
		t.Fatalf("scopes should be nil when claim absent; got %v", got)
	}
}

func TestScopesFromJWT_OpaqueToken(t *testing.T) {
	// Not a JWT — caller should treat unparseable tokens as
	// "no restriction" (the daemon's verified check still
	// applies on the real call).
	got, ok := scopesFromJWT("opaque-bearer-token")
	if ok {
		t.Fatal("expected parsed=false for non-JWT")
	}
	if got != nil {
		t.Fatalf("scopes should be nil for non-JWT; got %v", got)
	}
}

func TestToolAllowed_NilGrantsLetEverythingThrough(t *testing.T) {
	tool := &Tool{Name: "create_container", RequiredScope: auth.ScopeContainersWrite}
	if !toolAllowed(nil, tool) {
		t.Fatal("nil grants should be unrestricted (backwards compat)")
	}
}

func TestToolAllowed_RequiredScopeChecked(t *testing.T) {
	tool := &Tool{Name: "create_container", RequiredScope: auth.ScopeContainersWrite}
	if !toolAllowed([]string{auth.ScopeContainersWrite}, tool) {
		t.Fatal("granted scope should pass")
	}
	if toolAllowed([]string{auth.ScopeContainersRead}, tool) {
		t.Fatal("wrong scope should be rejected")
	}
}

func TestToolAllowed_EmptyRequiredAlwaysPasses(t *testing.T) {
	tool := &Tool{Name: "list_backends", RequiredScope: ""}
	if !toolAllowed([]string{}, tool) {
		t.Fatal("zero-scope tool should pass even on empty grants")
	}
}

// TestEveryToolHasScope guards against regressions where a
// new tool is added to registerTools without a scope
// assignment. The mapping in toolScopeAssignments() must
// list every registered tool. Compiles a fresh registry by
