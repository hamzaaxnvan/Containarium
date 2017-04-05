package zfskey

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"testing"
)

func mustKey(t *testing.T, b []byte) Key {
	t.Helper()
	k, err := NewKey(b)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func testKeyBytes(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, KeyLen)
}

// AC: key material never appears in logs, errors, or any String()/
// marshalled form.
//
// This is asserted rather than reviewed because every one of these is a
// path key bytes have escaped through in real systems: a %v in a log
// line, a %#v in a debug dump, a struct marshalled into an API response.
func TestKeyNeverRendersMaterial(t *testing.T) {
	secret := []byte("SUPERSECRETKEYMATERIAL0123456789") // exactly KeyLen
	if len(secret) != KeyLen {
		t.Fatalf("fixture is %d bytes, want %d", len(secret), KeyLen)
	}
	k := mustKey(t, secret)

	renderings := map[string]string{
		"%v":       fmt.Sprintf("%v", k),
		"%s":       fmt.Sprintf("key=%s", k),
		"%+v":      fmt.Sprintf("%+v", k),
		"%#v":      fmt.Sprintf("%#v", k),
		"%q":       fmt.Sprintf("%q", k),
		"String()": k.String(),
		"error":    fmt.Errorf("load failed for %v", k).Error(),
	}

	// A Key reached indirectly — nested in a struct — must redact too.
	type wrapper struct {
		Tenant string
		Key    Key
	}
	renderings["nested %v"] = fmt.Sprintf("%v", wrapper{Tenant: "alice", Key: k})
	renderings["nested %#v"] = fmt.Sprintf("%#v", wrapper{Tenant: "alice", Key: k})

	j, err := json.Marshal(wrapper{Tenant: "alice", Key: k})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	renderings["json"] = string(j)

