package auth

import (
	"net/http/httptest"
	"testing"
)

// Phase 1.5 — tests for ExtractBearerForUpgrade + AckSubprotocol.

// validJWT is a structurally valid JWT (header.payload.signature
// dot-separated, base64url alphabet). Signature is bogus —
// ExtractBearerForUpgrade does not verify, it just extracts.
const validJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.AAAA-_BBBBccccDDDDeeeeFFFFgggg"

func TestExtractBearerForUpgrade_Subprotocol_MarkerThenToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/x", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "containarium.bearer, "+validJWT)
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceSubprotocol {
		t.Fatalf("source: got %v want subprotocol", src)
	}
	if tok != validJWT {
		t.Fatalf("token: got %q want %q", tok, validJWT)
	}
}

func TestExtractBearerForUpgrade_Subprotocol_TokenWithoutMarker(t *testing.T) {
	// Some clients send the JWT alone — accepted as a fallback
	// because it still avoids the URL.
	r := httptest.NewRequest("GET", "/v1/x", nil)
	r.Header.Set("Sec-WebSocket-Protocol", validJWT)
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceSubprotocol {
		t.Fatalf("source: got %v want subprotocol", src)
	}
	if tok != validJWT {
		t.Fatalf("token: got %q want %q", tok, validJWT)
	}
}

func TestExtractBearerForUpgrade_Subprotocol_IgnoresNonJWTFallback(t *testing.T) {
	// A subprotocol entry that's NOT a JWT and NOT preceded by
	// the marker should be ignored (e.g. a real subprotocol the
	// client wants for the application layer).
	r := httptest.NewRequest("GET", "/v1/x", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "chat.v1, json")
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceNone {
		t.Fatalf("source: got %v want none; tok=%q", src, tok)
	}
}

func TestExtractBearerForUpgrade_Subprotocol_PrefersOverHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/x", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "containarium.bearer, "+validJWT)
	r.Header.Set("Authorization", "Bearer header-token")
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceSubprotocol {
		t.Fatalf("subprotocol must win over Authorization; got %v", src)
	}
	if tok != validJWT {
		t.Fatalf("token: got %q", tok)
	}
}

func TestExtractBearerForUpgrade_AuthorizationHeaderFallback(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/x", nil)
	r.Header.Set("Authorization", "Bearer hdr-token")
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceAuthorizationHeader {
		t.Fatalf("source: got %v", src)
	}
	if tok != "hdr-token" {
		t.Fatalf("token: got %q", tok)
	}
}

func TestExtractBearerForUpgrade_QueryParamLegacy(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/x?token=query-token", nil)
	tok, src := ExtractBearerForUpgrade(r)
	if src != TokenSourceQueryParam {
		t.Fatalf("source: got %v", src)
	}
	if tok != "query-token" {
		t.Fatalf("token: got %q", tok)
